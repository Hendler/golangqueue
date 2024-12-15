package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nsqio/go-nsq"
	"github.com/redis/go-redis/v9"
)

const NSQ_TOPIC_PREFIX = "factorization_topic"

type Worker struct {
	rdb       *redis.Client
	consumers map[string]*nsq.Consumer // map of callerID to consumer
	mu        sync.RWMutex             // to safely access the consumers map
	config    *nsq.Config
}

func NewWorker(rdb *redis.Client) *Worker {
	return &Worker{
		rdb:       rdb,
		consumers: make(map[string]*nsq.Consumer),
		config:    nsq.NewConfig(),
	}
}

// getOrCreateConsumer creates a new consumer for a callerID if it doesn't exist
func (w *Worker) getOrCreateConsumer(callerID string) (*nsq.Consumer, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if consumer, exists := w.consumers[callerID]; exists {
		return consumer, nil
	}

	// Create new consumer for this callerID
	topic := fmt.Sprintf("%s#%s", NSQ_TOPIC_PREFIX, callerID)
	consumer, err := nsq.NewConsumer(topic, "channel", w.config)
	if err != nil {
		return nil, fmt.Errorf("failed to create consumer for callerID %s: %v", callerID, err)
	}

	// Set the message handler
	consumer.AddHandler(nsq.HandlerFunc(w.ProcessMessage))

	// Connect to NSQ
	err = consumer.ConnectToNSQD("localhost:4150")
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NSQ for callerID %s: %v", callerID, err)
	}

	w.consumers[callerID] = consumer
	return consumer, nil
}

func (w *Worker) Start(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second) // Adjust the interval as needed
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.checkAndUpdateConsumers(ctx)
		}
	}
}

func (w *Worker) checkAndUpdateConsumers(ctx context.Context) {
	// Get all caller IDs from Redis
	callerIDs, err := w.rdb.SMembers(ctx, "caller_ids").Result()
	if err != nil {
		log.Printf("Error getting caller IDs from Redis: %v", err)
		return
	}

	// Create consumers for new caller IDs
	for _, callerID := range callerIDs {
		_, err := w.getOrCreateConsumer(callerID)
		if err != nil {
			log.Printf("Error creating consumer for callerID %s: %v", callerID, err)
			continue
		}
	}

	// Optionally, clean up consumers for caller IDs that no longer exist
	w.cleanupUnusedConsumers(callerIDs)
}

func (w *Worker) cleanupUnusedConsumers(activeCallerIDs []string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Create a map for quick lookup
	activeMap := make(map[string]bool)
	for _, id := range activeCallerIDs {
		activeMap[id] = true
	}

	// Stop and remove consumers that are no longer needed
	for callerID, consumer := range w.consumers {
		if !activeMap[callerID] {
			consumer.Stop()
			delete(w.consumers, callerID)
		}
	}
}

func (w *Worker) ProcessMessage(message *nsq.Message) error {
	var msg struct {
		CallerID  string `json:"caller_id"`
		RequestID string `json:"request_id"`
		Number    string `json:"number"`
	}

	if err := json.Unmarshal(message.Body, &msg); err != nil {
		return err
	}

	// Process the message...
	return nil
}

func main() {
	ctx := context.Background()

	// Initialize Redis client
	rdb := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	// Create worker
	worker := NewWorker(rdb)

	// Start the worker
	go worker.Start(ctx)

	// Wait forever
	select {}
}
