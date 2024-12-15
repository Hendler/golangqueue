Please email all deliverables to [jacky@runtrellis.com](mailto:jacky@runtrellis.com) and [mac@runtrellis.com](mailto:mac@runtrellis.com)

**Constraints:**

- You’re welcome to use any third-party libraries, AI tools, or projects (as long as there is no copyright on the code), etc.
- Implement the task in whatever language you’re most comfortable with.

### **Scenario:**

We are a company that processes computational requests on behalf of other businesses. Each client company integrates with our service using their unique `caller_id`. Our system must handle requests from these client companies fairly, ensuring that no single client can monopolize the service resources.

The caller does not need the results immediately. The caller does need to know when the results are done and get the results from the endpoint. You can design the web server ***and*** client interaction to gracefully handle sudden spikes in traffic. 

### **Endpoints:**

1. **`POST` Endpoint:**
    - **Functionality:** Accepts a computational request with the following property:
        - `number`: A large integer to be prime factorized (this task is cpu intensive)
            - More info [here](https://www.geeksforgeeks.org/print-all-prime-factors-of-a-given-number/)
        
        Here’s a number you can use for prime factorization: large_number = **100000000000000000050700000000000000004563**
        
        ```python
        import sympy
        large_number = 100000000000000000050700000000000000004563
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

### **Performance Requirements:**

- **Load Handling:** Your web server should handle at least 20,000 calls to both `POST` and `GET` endpoints within a span of 1 minute without failing (the higher the capacity, the better).
- We can throw more resources but let’s try to optimize how much we can achieve given `x` amount of resources.
- **Response Time:** All endpoints should return responses within 2 seconds. The `POST` endpoint doesn’t have to return the results immediately. The results-client interaction can be designed by you.
- **Reliability:**
    1. **No Request Drops:** Never drop or fail a request.
    2. **Graceful Handling of Spiky Load:** You can design the web server ***and*** client interaction to gracefully handle sudden spikes in traffic.

## **Deliverables:**

1. **All Code:**
    - Include all source code in a zipped file.
2. **Working Endpoints:**
    - Deploy the web server so that the `POST` and `GET` endpoints are operational.
    - Ensure that:
        - **Submit Factorization Task:** Live `POST` endpoint to submit a number for prime factorization with the required `caller_id`.
        - **Retrieve Result:** Live `GET` endpoint to retrieve the prime factors based on `request_id`.
3. **Documentation:**
    - Provide a README that includes:
        - **Setup Instructions:** How to set up and run the web server.
        - **Usage Instructions:** How to test the endpoints, including how to include the `caller_id` in the `POST` requests.