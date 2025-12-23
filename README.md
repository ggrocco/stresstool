# Distributed Stress Testing Tool

A flexible HTTP stress testing tool that supports both standalone and distributed execution modes.

## Architecture Overview

The tool has been refactored to support a **Controller-Node** architecture for distributed load testing:

### Components

1. **Controller**: 
   - Loads the test configuration file
   - Waits for worker nodes to connect
   - Distributes test specifications to all connected nodes
   - Coordinates test execution start
   - Aggregates and displays results from all nodes

2. **Node (Worker)**:
   - Connects to the controller
   - Receives test specifications
   - Executes tests with node-specific overrides
   - Reports progress and results back to the controller

3. **Standalone Mode**:
   - Traditional single-instance execution
   - No network communication required

## Usage

### 1. Standalone Mode (Single Machine)

Run tests directly without distribution:

```bash
./stresstool run -f config.yaml
```

Options:
- `-f, --file`: Path to YAML configuration file (required)
- `--verbose`: Print detailed logs
- `--dry-run`: Validate config without executing tests
- `--parallel`: Run all tests in parallel

### 2. Distributed Mode (Controller + Nodes)

#### Step 1: Start the Controller

```bash
./stresstool controller -f config.yaml --listen :8090
```

The controller will:
- Load the configuration
- Listen for node connections
- Wait for your command to start tests

Options:
- `-f, --file`: Path to YAML configuration file (required)
- `--listen`: Address to listen on (default: `:8090`)
- `--parallel`: Run tests in parallel on each node
- `--verbose`: Print detailed logs

#### Step 2: Start Worker Nodes

On each worker machine, run:

```bash
# Node A
./stresstool node --node-name node-a --controller controller-host:8090

# Node B
./stresstool node --node-name node-b --controller controller-host:8090

# Node C
./stresstool node --node-name node-c --controller controller-host:8090
```

Each node will:
- Connect to the controller
- Identify itself by name
- Wait for test specifications

Options:
- `--node-name`: Unique name for this node (required)
- `--controller`: Controller address to connect to (required)
- `--verbose`: Print detailed logs

#### Step 3: Start Tests from Controller

Once all nodes are connected, type `start` in the controller terminal:

```
Type 'start' when ready to begin tests, or 'nodes' to see connected nodes:
> nodes
Connected nodes (3):
  - node-a
  - node-b
  - node-c

> start
Starting tests on 3 node(s)...
```

The controller will:
1. Send test specifications to all nodes
2. Signal nodes to start simultaneously
3. Display real-time progress from all nodes
4. Aggregate and display final results

## Configuration

### Node-Specific Overrides

You can configure different load parameters for specific nodes in your YAML config:

```yaml
tests:
  - name: api_load_test
    path: "https://api.example.com/endpoint"
    method: "POST"
    requests_per_second: 100  # Default for all nodes
    threads: 10               # Default for all nodes
    run_seconds: 60
    
    # Node-specific overrides
    nodes:
      node-a:
        requests_per_second: 50
        threads: 5
      node-b:
        requests_per_second: 150
        threads: 15
      node-c:
        requests_per_second: 100
        # Uses default threads: 10
```

### Example Configuration

See `example-config.yaml` for a complete example:

```yaml
funcs:
  - name: token
    cmd: ["echo", "Bearer", "test-token-12345"]

tests:
  - name: api_login_test
    path: "https://httpbin.org/post"
    method: "POST"
    requests_per_second: 5
    threads: 2
    run_seconds: 10

    headers:
      Authorization: "{{ token() }}"
      X-Request-Id: "{{ uuid() }}"
      X-Timestamp: "{{ now() }}"
      Content-Type: "application/json"

    body: >
      {
        "username": "testuser",
        "timestamp": "{{ now() }}"
      }

    assert:
      status_code: 200
      body_contains: "json"
      max_latency_ms: 2000

    nodes:
      node-a:
        requests_per_second: 3
        threads: 1
      node-b:
        requests_per_second: 8
```

## Communication Protocol

The controller and nodes communicate using JSON messages over TCP:

### Message Types

1. **Hello**: Node announces itself to controller
2. **TestSpec**: Controller sends test configuration to node
3. **Ready**: Node confirms it's ready to start
4. **StartTests**: Controller signals all nodes to begin
5. **Progress**: Node sends real-time progress updates
6. **TestResult**: Node sends final test results
7. **Complete**: Controller signals all tests are done

## Security Considerations

⚠️ **IMPORTANT: The distributed mode has significant security implications that must be understood before use.**

### Threat Model

When using distributed mode (controller + nodes), be aware that:

1. **Configuration Data Exposure**: The controller sends the complete test configuration (including authorization headers, API keys, and custom command definitions) to all connected nodes over the network.

2. **Command Execution Risk**: Test configurations can include custom functions (`funcs`) that execute arbitrary operating system commands. If nodes accept configs from untrusted controllers, an attacker could:
   - Execute arbitrary commands on worker nodes
   - Exfiltrate sensitive data or credentials
   - Compromise the security of the worker systems

3. **Network Attacks**: Without TLS and mutual authentication:
   - Man-in-the-middle attacks can intercept sensitive data
   - Attackers can impersonate controllers or nodes
   - All communication is sent in plaintext

### Security Recommendations

#### 1. Use TLS with Mutual Authentication (Strongly Recommended)

**Always enable TLS with mutual authentication for production environments:**

```bash
# Generate certificates (example using OpenSSL)
# 1. Create CA
openssl req -x509 -newkey rsa:4096 -days 365 -nodes \
  -keyout ca-key.pem -out ca-cert.pem \
  -subj "/CN=StressTool CA"

# 2. Create controller certificate
openssl req -newkey rsa:4096 -nodes \
  -keyout controller-key.pem -out controller-req.pem \
  -subj "/CN=controller"
openssl x509 -req -in controller-req.pem -days 365 \
  -CA ca-cert.pem -CAkey ca-key.pem -CAcreateserial \
  -out controller-cert.pem

# 3. Create node certificates (repeat for each node)
openssl req -newkey rsa:4096 -nodes \
  -keyout node-key.pem -out node-req.pem \
  -subj "/CN=node1"
openssl x509 -req -in node-req.pem -days 365 \
  -CA ca-cert.pem -CAkey ca-key.pem -CAcreateserial \
  -out node-cert.pem
```

**Start controller with TLS:**
```bash
./stresstool controller -f config.yaml --listen :8090 \
  --tls \
  --tls-cert controller-cert.pem \
  --tls-key controller-key.pem \
  --tls-ca ca-cert.pem
```

**Start nodes with TLS:**
```bash
./stresstool node --node-name node1 --controller controller:8090 \
  --tls \
  --tls-ca ca-cert.pem \
  --tls-cert node-cert.pem \
  --tls-key node-key.pem
```

#### 2. Restrict Command Execution on Nodes

**Default Behavior (Secure)**: Nodes reject any configuration containing custom functions by default.

**To allow specific commands** (use with caution):
```bash
# Allow only specific safe commands
./stresstool node --node-name node1 --controller controller:8090 \
  --allow-remote-commands \
  --allowed-commands "echo,/usr/bin/date,/usr/bin/curl"
```

**To allow all commands** (UNSAFE - only on fully trusted networks):
```bash
./stresstool node --node-name node1 --controller controller:8090 \
  --allow-remote-commands
```

#### 3. Network Isolation

- **Use a dedicated private network** or VPN for controller-node communication
- **Implement firewall rules** to restrict access to controller ports
- **Never expose the controller** directly to the public internet
- **Use network segmentation** to isolate stress testing infrastructure

#### 4. Credential Management

- **Avoid embedding secrets** in config files when possible
- **Use environment variables** or secure secret management systems
- **Rotate credentials** regularly
- **Audit who has access** to config files and certificates

### Security Warning Messages

When TLS is disabled, you'll see:
```
⚠️  WARNING: TLS is not enabled. Communication is unencrypted and unauthenticated.
   This mode should only be used on trusted networks.
   Secrets in config (headers, auth tokens) will be sent in plaintext.
```

When remote commands are blocked, you'll see:
```
⚠️  SECURITY: Rejecting config due to unauthorized command
   The controller attempted to send a config with commands that are not allowed.
```

### Best Practices Summary

✅ **DO:**
- Always use TLS with mutual authentication in production
- Use the default secure command policy (block remote commands)
- Run on isolated, trusted networks
- Regularly audit and rotate certificates
- Keep sensitive configs protected

❌ **DON'T:**
- Use unencrypted mode over untrusted networks
- Allow arbitrary remote command execution
- Expose controller ports to the internet
- Share certificates across environments
- Store plaintext secrets in config files


## Benefits of Distributed Mode

1. **Higher Load**: Distribute load across multiple machines to test at scale
2. **Geographic Distribution**: Run nodes in different regions to test from multiple locations
3. **Node Specialization**: Configure different load profiles per node
4. **Centralized Control**: Single point to coordinate and monitor all tests
5. **Aggregated Results**: View combined results from all nodes in one place

## Example Distributed Test Scenario

```bash
# Terminal 1: Start Controller
./stresstool controller -f load-test.yaml --listen :8090

# Terminal 2: Node in US-East
./stresstool node --node-name us-east --controller localhost:8090

# Terminal 3: Node in US-West
./stresstool node --node-name us-west --controller localhost:8090

# Terminal 4: Node in EU
./stresstool node --node-name eu-central --controller localhost:8090

# Back to Terminal 1: Check nodes and start
> nodes
Connected nodes (3):
  - us-east
  - us-west
  - eu-central

> start
```

The controller will coordinate all three nodes to execute tests simultaneously, with each node applying its specific configuration overrides.

## Monitoring

### Real-Time Progress

During test execution, the controller displays live updates from all nodes:

```
→ us-east / api_login_test: 5s elapsed - 25 requests, 5.0 RPS, 0 failures
→ us-west / api_login_test: 5s elapsed - 40 requests, 8.0 RPS, 1 failures
→ eu-central / api_login_test: 5s elapsed - 15 requests, 3.0 RPS, 0 failures
```

### Final Results

After all tests complete, view aggregated metrics per node:

```
=== Node: us-east ===
Test: api_login_test
  Requests: 50 total, 50 success, 0 failures
  Latency: Min: 45ms, Avg: 123ms, Max: 456ms, P95: 234ms, P99: 345ms
  Result: ✓ PASSED

=== Node: us-west ===
...
```

## Build from Source

```bash
git clone <repository-url>
cd stresstool
go build -o stresstool ./cmd/stresstool
```

### Install Globally
```bash
go install ./cmd/stresstool
```

## Architecture Benefits

- **Separation of Concerns**: Controller handles coordination, nodes handle execution
- **Scalability**: Add more nodes to increase load capacity
- **Flexibility**: Mix standalone and distributed modes as needed
- **Observability**: Centralized progress monitoring and result aggregation
