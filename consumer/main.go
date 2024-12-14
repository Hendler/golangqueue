package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/nsqio/go-nsq"
	"github.com/redis/go-redis/v9"
)

// Job represents a factorization job.
type Job struct {
	CallerID  string
	RequestID string
}

// FairScheduler implements a round-robin scheduler over multiple caller queues.
type FairScheduler struct {
	mu          sync.Mutex
	callerOrder []string
	queues      map[string][]*Job
	pos         int
}

func NewFairScheduler() *FairScheduler {
	return &FairScheduler{
		queues: make(map[string][]*Job),
	}
}

func (f *FairScheduler) AddJob(j *Job) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.queues[j.CallerID]; !ok {
		// This is a new caller we haven't seen before.
		f.queues[j.CallerID] = []*Job{j}
		f.callerOrder = append(f.callerOrder, j.CallerID)
	} else {
		f.queues[j.CallerID] = append(f.queues[j.CallerID], j)
	}
}

// NextJob returns the next job in a round-robin fashion, or nil if none available.
func (f *FairScheduler) NextJob() *Job {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.callerOrder) == 0 {
		return nil
	}

	startPos := f.pos
	for {
		caller := f.callerOrder[f.pos]
		q := f.queues[caller]

		if len(q) > 0 {
			// Pop the first job
			job := q[0]
			f.queues[caller] = q[1:]
			// If that caller’s queue is now empty, remove them from the order.
			if len(f.queues[caller]) == 0 {
				delete(f.queues, caller)
				f.callerOrder = append(f.callerOrder[:f.pos], f.callerOrder[f.pos+1:]...)
				// Adjust position if needed
				if f.pos >= len(f.callerOrder) && len(f.callerOrder) > 0 {
					f.pos = f.pos % len(f.callerOrder)
				}
			} else {
				// Move position forward
				f.pos = (f.pos + 1) % len(f.callerOrder)
			}
			return job
		} else {
			// This caller has no jobs, remove from order
			delete(f.queues, caller)
			f.callerOrder = append(f.callerOrder[:f.pos], f.callerOrder[f.pos+1:]...)
			if len(f.callerOrder) == 0 {
				return nil
			}
			if f.pos >= len(f.callerOrder) {
				f.pos = 0
			}
		}

		// If we've looped around and found no jobs, return nil
		if f.pos == startPos {
			return nil
		}
	}
}

type MessageHandler struct {
	scheduler *FairScheduler
}

func (h *MessageHandler) HandleMessage(m *nsq.Message) error {
	// Parse the message body. Expected format: caller_id:request_id
	// Adjust parsing logic as per your message format.
	body := string(m.Body)
	var callerID, requestID string
	fmt.Sscanf(body, "%s:%s", &callerID, &requestID)

	h.scheduler.AddJob(&Job{
		CallerID:  callerID,
		RequestID: requestID,
	})
	return nil
}

func main() {
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{
		Addr: "trellisredis:6379",
	})

	scheduler := NewFairScheduler()

	// Create NSQ consumer
	cfg := nsq.NewConfig()
	consumer, err := nsq.NewConsumer("factorization_topic", "channel", cfg)
	if err != nil {
		log.Fatal(err)
	}
	consumer.AddHandler(&MessageHandler{scheduler: scheduler})

	// Connect to NSQ lookup or nsqd directly
	err = consumer.ConnectToNSQLookupd("nsqlookupd:4161")
	if err != nil {
		log.Fatal(err)
	}

	// Worker pool to process jobs concurrently
	numWorkers := 10
	var wg sync.WaitGroup
	wg.Add(numWorkers)

	for i := 0; i < numWorkers; i++ {
		go func(workerID int) {
			defer wg.Done()
			for {
				job := scheduler.NextJob()
				if job == nil {
					// No jobs available, sleep a bit to prevent busy looping
					time.Sleep(50 * time.Millisecond)
					continue
				}
				// Process the job
				processJob(ctx, rdb, job)
			}
		}(i)
	}

	wg.Wait()
}

// processJob retrieves the job from Redis, factorizes the number, and stores the result.
func processJob(ctx context.Context, rdb *redis.Client, job *Job) {
	// Get job details from Redis
	values, err := rdb.HGetAll(ctx, "request:"+job.RequestID).Result()
	if err != nil || len(values) == 0 {
		log.Printf("Error retrieving job %s: %v", job.RequestID, err)
		return
	}

	numberStr := values["number"]
	if numberStr == "" {
		log.Printf("No number found for request %s", job.RequestID)
		return
	}

	// Perform factorization (placeholder logic)
	// In a real scenario, factor the big number. Here we just simulate.
	// Implement a real factorization or call out to a prime factorization function.
	result := map[string]int{
		"2":  1,
		"5":  2,
		"11": 1,
	}
	// Format result as string for storage
	resultStr := ""
	for prime, count := range result {
		resultStr += fmt.Sprintf("%s:%d\n", prime, count)
	}

	// Store results back in Redis
	rdb.HSet(ctx, "request:"+job.RequestID,
		"status", "complete",
		"result", resultStr,
	)
	log.Printf("Completed factorization for request %s from caller %s", job.RequestID, job.CallerID)
}
