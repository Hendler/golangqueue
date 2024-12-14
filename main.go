/*
*
1. **`POST` Endpoint:**

  - **Functionality:** Accepts a computational request with the following property:

  - `number`: A large integer to be prime factorized (this task is cpu intensive)

  - More info [here](https://www.geeksforgeeks.org/print-all-prime-factors-of-a-given-number/)

    Here’s a number you can use for prime factorization: large_number = **100000000000000000050700000000000000004563**

    ```python
    import sympy
    prime_factors = sympy.factorint(large_number)
    ```

  - **Parameters:**

  - `caller_id` (Header or Query Parameter): Identifies the client company making the request. Assume that any `caller_id` is a valid `caller_id`.

  - **Response:** Returns a `request_id` associated with the factorization task.

  - **Fairness Among Callers:** Implement a mechanism to ensure that all client companies (`caller_id`s) are treated fairly, preventing any single client from overwhelming the service. For instance, if `caller_1` has 500 requests, `caller_2`'s request ***should not*** have to wait for all 500 `caller_1` requests to complete. This is typically called round robin.

2. **`GET` Endpoint:**
  - **Functionality:** Accepts a `request_id` and returns the prime factorization result of the associated number.
  - **Parameters:**
  - `request_id` (Path or Query Parameter): The identifier for the prime factorization task.
  - **Response:** Returns the prime factors of the number submitted in the corresponding `POST` request.
*/

package main

import (
	"context"
	"fmt"
	"strings"

	"math/big"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type ComputeRequest struct {
	Number *big.Int `json:"number"`
}

type ComputeResponse struct {
	RequestID string `json:"request_id"`
}

type ResultResponse struct {
	Status string         `json:"status"`
	Result map[string]int `json:"result,omitempty"`
}

func main() {
	app := fiber.New()
	rdb := redis.NewClient(&redis.Options{
		Addr: "trellisredis:6379",
	})

	app.Post("/compute", func(c *fiber.Ctx) error {
		callerID := c.Get("X-Caller-ID")
		if callerID == "" {
			return c.Status(400).JSON(fiber.Map{"error": "Missing caller ID"})
		}

		var req ComputeRequest
		if err := c.BodyParser(&req); err != nil {
			return err
		}

		requestID := uuid.New().String()

		// Store in Redis
		ctx := context.Background()
		rdb.HSet(ctx, "request:"+requestID,
			"caller_id", callerID,
			"status", "pending",
			"number", req.Number,
		)

		// Add to caller's queue
		rdb.RPush(ctx, "caller_queue:"+callerID, requestID)

		return c.JSON(ComputeResponse{RequestID: requestID})
	})

	app.Get("/compute/:requestID", func(c *fiber.Ctx) error {
		requestID := c.Params("requestID")
		if requestID == "" {
			return c.Status(400).JSON(fiber.Map{"error": "Missing request ID"})
		}

		// Get request data from Redis
		ctx := context.Background()
		values, err := rdb.HGetAll(ctx, "request:"+requestID).Result()
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "Internal server error"})
		}
		if len(values) == 0 {
			return c.Status(404).JSON(fiber.Map{"error": "Request not found"})
		}

		// Check status and return appropriate response
		status := values["status"]
		if status == "pending" {
			return c.JSON(ResultResponse{
				Status: "pending",
			})
		}

		// If complete, parse and return the results
		if status == "complete" {
			resultStr := values["result"]
			result := make(map[string]int)

			// Parse the stored result string into our map
			for _, pair := range strings.Split(resultStr, "\n") {
				if pair == "" {
					continue
				}
				var prime string
				var count int
				fmt.Sscanf(pair, "%s:%d", &prime, &count)
				result[prime] = count
			}

			return c.JSON(ResultResponse{
				Status: "complete",
				Result: result,
			})
		}

		return c.Status(500).JSON(fiber.Map{"error": "Invalid request status"})
	})

	port := ":5555"
	fmt.Printf("Starting server on port %s\n", port)
	app.Listen(port)
}
