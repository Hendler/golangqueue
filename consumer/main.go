package main

import (
	"context"
	"encoding/json"
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

func main() {
	// Initialize NSQ consumer and producer
	config := nsq.NewConfig()
	consumer, err := nsq.NewConsumer(NSQ_TOPIC_PREFIX, "channel", config)
	if err != nil {
		log.Fatal("Could not create NSQ consumer:", err)
	}

	producer, err := nsq.NewProducer(NSQ_SERVER, config)
	if err != nil {
		log.Fatal("Could not create NSQ producer:", err)
	}

	// Initialize Redis client
	rdb := redis.NewClient(&redis.Options{
		Addr: os.Getenv("REDIS_URL"),
	})

	// IMPORTANT: Add the handler BEFORE connecting to NSQLookupd
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
		// total_count, err := rdb.Incr(ctx, "total_count").Result()
		// if err != nil {
		// 	err = rdb.Set(ctx, "total_count", 1, 0).Err()
		// 	if err != nil {
		// 		log.Printf("Failed to set total_count: %v", err)
		// 	}
		// 	total_count = 1
		// }
		// log.Printf("Total count: %d", total_count)

		// err = rdb.SAdd(ctx, "caller_ids", msg.CallerID).Err()
		// if err != nil {
		// 	log.Printf("Failed to add caller ID to set: %v", err)
		// }

		// Your existing logic
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

	// Second loop: Monitor NSQ_PRIORITY_TOPIC queue size
	for {
		// Get queue depth for priority topic
		resp, err := http.Get(NSQLOOKUPD_STATS_URL)
		if err != nil {
			log.Printf("Error getting NSQ stats: %v", err)
			time.Sleep(time.Second)
			continue
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
			time.Sleep(time.Second)
			continue
		}

		var currentQueueSize int64
		for _, topic := range nsqStats.Topics {
			if topic.TopicName == NSQ_PRIORITY_TOPIC {
				currentQueueSize = topic.Depth
				break
			}
		}
		log.Printf("Current queue size for topic %s: %d", NSQ_PRIORITY_TOPIC, currentQueueSize)

		if currentQueueSize < MAX_NSQ_PRIORITY_TOPIC_SIZE {
			// Create context
			ctx := context.Background()
			batch_count := 0
			available_count := 0
			for batch_count < BATCH_SIZE {
				// Select a request ID to process
				caller_counts, err := rdb.Keys(ctx, "caller_count:*").Result()
				if err != nil {
					log.Printf("Failed to get caller counts: %v", err)
					time.Sleep(time.Second)
					continue
				}
				// get the caller_count for each caller_count
				for _, caller_count_key := range caller_counts {
					// Extract caller ID from key (remove "caller_count:" prefix)
					callerID := strings.TrimPrefix(caller_count_key, "caller_count:")

					// Get count value for this caller
					count, err := rdb.Get(ctx, caller_count_key).Result()
					if err != nil {
						log.Printf("Failed to get count for caller %s: %v", callerID, err)
						continue
					}
					// err = rdb.Del(ctx, caller_count_key).Err()
					// if err != nil {
					// 	log.Printf("Failed to delete caller count key %s: %v", caller_count_key, err)
					// 	continue
					// }
					countInt, err := strconv.Atoi(count)
					if err != nil {
						log.Printf("Failed to convert count to int: %v", err)
						continue
					}
					if countInt > 0 {
						available_count += countInt
					}

					log.Printf("Caller: %s, Count: %s", callerID, count)

					if available_count > 0 {
						// Get highest scored request ID from caller's priority queue
						results, err := rdb.ZRevRangeWithScores(ctx, "priority-"+callerID, 0, 0).Result()
						if err != nil {
							log.Printf("Failed to get request ID from priority queue: %v", err)
							continue
						}
						if len(results) > 0 {
							requestID := results[0].Member.(string)

							// Publish to priority topic
							err := producer.Publish(NSQ_PRIORITY_TOPIC, []byte(requestID))
							if err != nil {
								log.Printf("Error publishing to priority topic: %v", err)
								continue // Skip removing if publish failed
							}

							// Remove the request ID from the sorted set
							err = rdb.ZRem(ctx, "priority-"+callerID, requestID).Err()
							if err != nil {
								log.Printf("Failed to remove request ID from priority queue: %v", err)
								// You might want to handle this error case depending on your requirements
							}

							// decrement the caller count

							err = rdb.Decr(ctx, caller_count_key).Err()
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
				if available_count == 0 {
					break
				}
			}
			time.Sleep(time.Second) // Add delay to prevent tight loop
		}

	}
}
