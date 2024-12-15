package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nsqio/go-nsq"
	"github.com/redis/go-redis/v9"
)

const NSQLOOKUPD_STATS_URL = "http://nsqlookupd:4161/nodes"
const NSQ_SERVER = "nsqd1:4150"
const NSQ_TOPIC_PREFIX = "factorization_topic"
const NSQ_PRIORITY_TOPIC string = "factorization_priority_topic"
const MAX_NSQ_PRIORITY_TOPIC_SIZE = 1000
const BATCH_SIZE = 100

// there is only meant to be one of these coordinators for now, mostly because of redis
type WorkerCoordinator struct {
	Consumer *nsq.Consumer
	Producer *nsq.Producer
	Rdb      *redis.Client
}

func NewWorkerCoordinator() (*WorkerCoordinator, error) {
	config := nsq.NewConfig()

	consumer, err := nsq.NewConsumer(NSQ_TOPIC_PREFIX, "channel", config)
	if err != nil {
		return nil, fmt.Errorf("could not create NSQ consumer: %w", err)
	}

	producer, err := nsq.NewProducer(NSQ_SERVER, config)
	if err != nil {
		consumer.Stop()
		return nil, fmt.Errorf("could not create NSQ producer: %w", err)
	}

	// Create the priority topic if it doesn't exist
	err = producer.Publish(NSQ_PRIORITY_TOPIC, []byte("init"))
	if err != nil {
		log.Printf("Failed to create priority topic: %v", err)
	}

	rdb := redis.NewClient(&redis.Options{
		Addr: os.Getenv("REDIS_URL"),
	})

	consumer.AddHandler(nsq.HandlerFunc(func(message *nsq.Message) error {
		message.Touch()
		// Parse message into struct
		type Message struct {
			CallerID  string `json:"caller_id"`
			RequestID string `json:"request_id"`
			Number    string `json:"number"`
		}

		var msg Message
		err := json.Unmarshal(message.Body, &msg)
		if err != nil {
			log.Printf("Error unmarshaling message: %v", err)
			return err
		}

		log.Printf("Received message - CallerID: %s, RequestID: %s, Number: %s",
			msg.CallerID, msg.RequestID, msg.Number)
		// store some stats in the background
		ctx := context.Background()

		count, err := rdb.Incr(ctx, "caller_count:"+msg.CallerID).Result()
		if err != nil {
			err = rdb.Set(ctx, "caller_count:"+msg.CallerID, 1, 0).Err()
			if err != nil {
				log.Printf("Failed to set caller count: %v", err)
			}
			count = 1
		}

		// Add request to caller priority queue with count as score
		err = rdb.ZAdd(ctx, "priority-"+msg.CallerID, redis.Z{
			Score:  float64(1 / count), // this is the scoring to preserve LIFO per caller
			Member: msg.RequestID,
		}).Err()
		if err != nil {
			log.Printf("Failed to add to caller priority queue: %v", err)
		}
		// Store request data, this will be the state used by client, and state for storing results
		err = rdb.HSet(ctx, "request:"+msg.RequestID,
			"caller_id", msg.CallerID,
			"status", "pending",
			"number", msg.Number,
			"processed_at_count", count,
			"results", "",
		).Err()
		if err != nil {
			log.Printf("Failed to store request data in Redis: %v", err)
			message.Requeue(-1) // Requeue message with default timeout
			return nil
		}
		// Mark message as finished in NSQ
		message.Finish()
		return nil
	}))

	// After creating the consumer
	log.Printf("Consumer created for topic: %s", NSQ_TOPIC_PREFIX)

	// After adding the handler
	log.Printf("Handler registered for topic: %s", NSQ_TOPIC_PREFIX)

	// Connect to NSQLookupd AFTER adding the handler
	err = consumer.ConnectToNSQLookupd("nsqlookupd:4161")
	if err != nil {
		log.Fatal("Could not connect to NSQ lookup:", err)
	}
	log.Printf("Successfully connected to NSQLookupd")

	// You might also want to add a stats handler to monitor the consumer
	consumer.SetLogger(log.New(os.Stdout, "", log.LstdFlags), nsq.LogLevelDebug)

	return &WorkerCoordinator{
		Consumer: consumer,
		Producer: producer,
		Rdb:      rdb,
	}, nil
}

// GetQueueSize queries the NSQ lookup daemon's stats endpoint to get the current size
// of the priority queue topic. It makes an HTTP request to the NSQLookupd stats URL
// and parses the response to find the depth (number of messages) of the priority topic.
//
// The function returns:
// - The number of messages currently in the priority topic queue
// - 0 if there are any errors or if the priority topic is not found
//
// This is used to implement backpressure - ensuring the priority queue doesn't grow
// too large by checking its size before adding more messages.
func (wc *WorkerCoordinator) GetQueueSize() int64 {
	resp, err := http.Get(NSQLOOKUPD_STATS_URL)
	if err != nil {
		log.Printf("Error getting NSQ stats: %v", err)
		return 0
	}
	defer resp.Body.Close()

	var nsqStats struct {
		Topics []struct {
			TopicName string `json:"topic_name"`
			Depth     int64  `json:"depth"`
		} `json:"topics"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&nsqStats); err != nil {
		log.Printf("Error decoding NSQ stats: %v", err)
		return 0
	}

	for _, topic := range nsqStats.Topics {
		if topic.TopicName == NSQ_PRIORITY_TOPIC {
			log.Printf("Current queue size for topic %s: %d", NSQ_PRIORITY_TOPIC, topic.Depth)
			return topic.Depth
		}
	}

	return 0
}

func main() {
	// Initialize NSQ consumer and producer
	workerCoordinator, err := NewWorkerCoordinator()
	if err != nil {
		log.Fatal("Could not create worker coordinator:", err)
	}

	// Second loop: Monitor NSQ_PRIORITY_TOPIC queue size
	for {
		currentQueueSize := workerCoordinator.GetQueueSize()
		if currentQueueSize >= MAX_NSQ_PRIORITY_TOPIC_SIZE {
			time.Sleep(time.Second) // Add delay when queue is full
			continue
		}

		ctx := context.Background()
		batch_count := 0
		available_count := 0

		// Process one batch
		for batch_count < BATCH_SIZE {
			// Select a request ID to process
			caller_counts, err := workerCoordinator.Rdb.Keys(ctx, "caller_count:*").Result()
			if err != nil {
				log.Printf("Failed to get caller counts: %v", err)
				time.Sleep(time.Second)
				continue
			}

			// Check if there are any callers with counts > 0
			found_work := false

			// get the caller_count for each caller_count
			for _, caller_count_key := range caller_counts {
				// Extract caller ID from key (remove "caller_count:" prefix)
				callerID := strings.TrimPrefix(caller_count_key, "caller_count:")

				// Get count value for this caller
				count, err := workerCoordinator.Rdb.Get(ctx, caller_count_key).Result()
				if err != nil {
					log.Printf("Failed to get count for caller %s: %v", callerID, err)
					continue
				}

				countInt, err := strconv.Atoi(count)
				if err != nil {
					log.Printf("Failed to convert count to int: %v", err)
					continue
				}
				if countInt > 0 {
					found_work = true
					available_count += countInt
				}

				// log.Printf("Caller: %s, Count: %s", callerID, count)

				if available_count > 0 {
					// Get highest scored request ID from caller's priority queue
					results, err := workerCoordinator.Rdb.ZRevRangeWithScores(ctx, "priority-"+callerID, 0, 0).Result()
					if err != nil {
						log.Printf("Failed to get request ID from priority queue: %v", err)
						continue
					}
					if len(results) > 0 {
						requestID := results[0].Member.(string)

						// Publish to priority topic
						err := workerCoordinator.Producer.Publish(NSQ_PRIORITY_TOPIC, []byte(requestID))
						if err != nil {
							log.Printf("Error publishing to priority topic: %v", err)
							continue // Skip removing if publish failed
						}

						// Remove the request ID from the sorted set
						err = workerCoordinator.Rdb.ZRem(ctx, "priority-"+callerID, requestID).Err()
						if err != nil {
							log.Printf("Failed to remove request ID from priority queue: %v", err)
							// You might want to handle this error case depending on your requirements
						}

						// decrement the caller count

						err = workerCoordinator.Rdb.Decr(ctx, caller_count_key).Err()
						if err != nil {
							log.Printf("Failed to decrement caller count: %v", err)
						}

						batch_count++
						available_count--
					}
				} else {
					log.Printf("Caller %s has no requests to process, available_count: %d", callerID, available_count)
				}
			}

			// Break out if no work was found
			if !found_work {
				time.Sleep(time.Second)
			}

			if available_count == 0 {
				time.Sleep(time.Second)
			}
		}

	}
}
