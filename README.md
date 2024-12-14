# Setup

Read the [SETUP.md](SETUP.md) file for instructions on how to set up the development environment. 

# design decisions

- chose go because it's easier than rust, faster than python, better memory and concurrency model than python
-  NSQ for the queue because it's easier to scale than redis for this usecase and easier, easier to integrate with go and easier to setup than kafka
-  redis because it's easy to setup and easy to use
- use string instead of int64 to let int parsing happen later

# TODO

 - parse large string to big number
 - safer input
 - worker service that autoscales to handle nsq queue size
 - make worker service in docker

# UNFINISHED

- put haproxy in front of webserver to load balance - or if on cloud use other load balancer 
- build a deployment script and k8 pipeline to autoscale each component
- use a cloud service for hosted redis and sqs for example
- better configuration management of ports across app
- use docker swarm to scale nsq , since I can use replicas attribute  
- for faster dev, bind source code and hot reload/rebuild go server

# Running the app

    run docker compose up -d

Then you can see that nsq is up at http://localhost:4171/ and see nodes at http://localhost:4171/nodes to verify it's up.

to check redis is up and working, you can hit 
http://localhost:8004/

to check webserver is up and working, you can hit 
http://localhost:5555/compute/230432

# hit 
curl -X POST http://localhost:5555/compute \
  -H "Content-Type: application/json" \
  -H "X-Caller-ID: test-caller-1" \
  -d '{"number": "100000000000000000050700000000000000004563"}'


# work log

 - worked friday dec 13 from 6 to 7pm
 