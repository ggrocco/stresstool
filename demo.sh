#!/bin/bash
# Demo script for distributed testing

echo "=== Distributed Stress Test Demo ==="
echo ""
echo "This demo will start a controller and two nodes to demonstrate"
echo "the distributed testing capabilities."
echo ""

# Build if needed
if [ ! -f "./stresstool" ]; then
    echo "Building stresstool..."
    go build -o stresstool ./cmd/stresstool/
fi

# Start controller in background
echo "Starting controller..."
./stresstool controller -f example-config.yaml --listen :8090 > controller.log 2>&1 &
CONTROLLER_PID=$!
sleep 2

# Start node-a in background
echo "Starting node-a..."
NODE_NAME=node-a ./stresstool node --node-name node-a --controller localhost:8090 > node-a.log 2>&1 &
NODE_A_PID=$!
sleep 1

# Start node-b in background
echo "Starting node-b..."
NODE_NAME=node-b ./stresstool node --node-name node-b --controller localhost:8090 > node-b.log 2>&1 &
NODE_B_PID=$!
sleep 1

echo ""
echo "All components started:"
echo "  - Controller (PID: $CONTROLLER_PID)"
echo "  - Node A (PID: $NODE_A_PID)"
echo "  - Node B (PID: $NODE_B_PID)"
echo ""
echo "To start the tests, you need to manually send 'start' to the controller."
echo "For now, check the logs:"
echo "  - tail -f controller.log"
echo "  - tail -f node-a.log"
echo "  - tail -f node-b.log"
echo ""
echo "To clean up, run: kill $CONTROLLER_PID $NODE_A_PID $NODE_B_PID"
