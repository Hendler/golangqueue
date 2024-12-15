package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"

	"github.com/redis/go-redis/v9"

	"github.com/nsqio/go-nsq"
)

const NSQ_LOOKUPD_SERVER = "nsqlookupd:4161"
const NSQ_PRIORITY_TOPIC string = "factorization_priority_topic"

var NUMBER_OF_WORKERS = runtime.NumCPU() * 5

// there is only meant to be one of these coordinators for now, mostly because of redis
type Worker struct {
	Consumer *nsq.Consumer
	Rdb      *redis.Client
}

func (wc *Worker) GetQueueSize() int64 {
	resp, err := http.Get(NSQ_PRIORITY_TOPIC)
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
		} else {
			log.Printf("Topic %s not found", topic.TopicName)
			return 0
		}
	}

	return 0
}

func NewWorker() (*Worker, error) {
	config := nsq.NewConfig()

	consumer, err := nsq.NewConsumer(NSQ_PRIORITY_TOPIC, "channel", config)
	if err != nil {
		return nil, fmt.Errorf("could not create NSQ consumer: %w", err)
	}

	rdb := redis.NewClient(&redis.Options{
		Addr: os.Getenv("REDIS_URL"),
	})

	// After creating the consumer
	log.Printf("Consumer created for topic: %s", NSQ_PRIORITY_TOPIC)
	consumer.AddHandler(nsq.HandlerFunc(func(message *nsq.Message) error {
		message.Touch()
		// Parse message into struct
		requestID := string(message.Body)

		// Get all fields from Redis hash
		ctx := context.Background()
		values, err := rdb.HGetAll(ctx, "request:"+requestID).Result()
		if err != nil {
			log.Printf("Failed to get request data from Redis: %v", err)
			message.Requeue(-1)
			return nil
		}

		// Parse the values into the struct
		callerID := values["caller_id"]
		// status := values["status"]
		number := values["number"]
		count := values["processed_at_count"]

		log.Printf("Processing request - CallerID: %s, Number: %s", callerID, number)

		// // store some stats in the background
		// count, err := rdb.Decr(ctx, "caller_count:"+callerID).Result()
		// if err != nil {
		// 	err = rdb.Set(ctx, "caller_count:"+callerID, 0, 0).Err()
		// 	if err != nil {
		// 		log.Printf("Failed to set caller count: %v", err)
		// 	}
		// 	count = 0
		// }

		// Store request data, this will be the state used by client, and state for storing results
		err = rdb.HSet(ctx, "request:"+requestID,
			"caller_id", callerID,
			"status", "completed",
			"number", number,
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

	// Connect to NSQLookupd AFTER adding the handler
	err = consumer.ConnectToNSQLookupd(NSQ_LOOKUPD_SERVER)
	if err != nil {
		log.Fatal("Could not connect to NSQ lookup:", err)
	}
	log.Printf("Successfully connected to NSQLookupd")

	// You might also want to add a stats handler to monitor the consumer
	consumer.SetLogger(log.New(os.Stdout, "", log.LstdFlags), nsq.LogLevelDebug)

	return &Worker{
		Consumer: consumer,
		Rdb:      rdb,
	}, nil

}

func main() {
	instances := make([]*Worker, NUMBER_OF_WORKERS)
	for i := 0; i < NUMBER_OF_WORKERS; i++ {
		instance, err := NewWorker()
		if err != nil {
			log.Printf("Failed to create worker: %v", err)
		}
		instances[i] = instance
	}

}
