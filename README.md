Distributed Bitcoin Miner
==

This repository contains the completed code for project 1 (15-440, Spring 2014). It also contains
the tests that used to grade. The details could be found in the handout file `p1.pdf`

If at any point you have any trouble with building, installing, or testing your code, the article
titled [How to Write Go Code](http://golang.org/doc/code.html) is a great resource for understanding
how Go workspaces are built and organized. You might also find the documentation for the
[`go` command](http://golang.org/cmd/go/) to be helpful.
on Piazza.

## Part A: LSP Proctol
LSP provides features that lie somewhere between UDP and TCP, but it also has features not found in either protocol:
• Unlike UDP or TCP, it supports a client-server communication model.
• The server maintains connections between a number of clients, each of which is identified by a numeric connection identifier.
• Communication between the server and a client consists of a sequence of discrete messages in each direction.
• Message sizes are limited to fit within single UDP packets (around 1,000 bytes).
• Messages are sent reliably: a message sent over the network must be received exactly once, and messages must be received in the same order they were sent.
• The server and clients monitor the status of their connections and detect when the other side has become disconnected.

`github/cmu440/lsp` contains the API and the implementation of a LSP server and client.

### Compile and run
To compile, build, and run these programs, use the `go run` command from inside the directory
storing the file (these instructions assume your `GOPATH` is pointing to the project's root
`p1/` directory):

```bash
go run srunner.go
```

The `srunner` and `crunner` programs may be customized using command line flags. For more
information, specify the `-h` flag at the command line. For example,

```bash
$ go run srunner.go -h
Usage of bin/srunner:
  -elim=5: epoch limit
  -ems=2000: epoch duration (ms)
  -port=9999: port number
  -rdrop=0: network read drop percent
  -v=false: show srunner logs
  -wdrop=0: network write drop percent
  -wsize=1: window size
```
### Running the tests

To test your submission, we will execute the following command from inside the
`p1/src/github.com/cmu440/lsp` directory for each of the tests (where `TestName` is the
name of one of the 44 test cases, such as `TestBasic6` or `TestWindow1`):

```sh
go test -run=TestName
```

Note that we will execute each test _individually_ using the `-run` flag and by specify a regular expression
identifying the name of the test to run. To ensure that previous tests don't affect the outcome of later tests,
we recommend executing the tests individually (or in small batches, such as `go test -run=TestBasic` which will
execute all tests beginning with `TestBasic`) as opposed to all together using `go test`.

On some tests, we will also check your code for race conditions using Go's race detector:

```sh
go test -race -run=TestName
```

## Part B: Distributed Bitcoin Miner
The mining infrastructure is implemented based upon a simplified variant of Bitcoin protocal: given a message M and an unsigned integer N, miners try to find the unsigned integer n which, when concatenated with M, generates the smallest hash value, for all 0 ≤ n ≤ N.

The distributed system upon the LSP protocal. The distributed system consists of the following three components:
Client: An LSP client that sends a user-specified request to the server, receives and prints the result, and then exits.
Miner: An LSP client that continually accepts requests from the server, exhaustively computes all hashes over a specified range of nonces, and then sends the server the final result.
Server: An LSP server that manages the entire Bitcoin cracking enterprise. At any time, the server can have any number of workers available, and can receive any number of requests from any number of clients. For each client request, it splits the request into multiple smaller jobs and distributes these jobs to its available miners. The server waits for each worker to respond before generating and sending the final result back to the client.

`github/cmu440/bitcoin` contains the implementation of the distributed system.

### Compiling the `client`, `miner` & `server` programs

To compile the `client`, `miner`, and `server` programs, use the `go install` command
as follows (these instructions assume your
`GOPATH` is pointing to the project's root `p1/` directory):

```bash
# Compile the client, miner, and server programs. The resulting binaries
# will be located in the $GOPATH/bin directory.
go install github.com/cmu440/bitcoin/client
go install github.com/cmu440/bitcoin/miner
go install github.com/cmu440/bitcoin/server

# Start the server, specifying the port to listen on.
$GOPATH/bin/server 6060

# Start a miner, specifying the server's host:port.
$GOPATH/bin/miner localhost:6060

# Start the client, specifying the server's host:port, the message
# "bradfitz", and max nonce 9999.
$GOPATH/bin/client localhost:6060 bradfitz 9999
```

### Running the tests

To execute the tests, make sure your `GOPATH` is properly set and then execute them as follows (note
that you for each binary, you can activate verbose-mode by specifying the `-v` flag). _Make sure you
compile your `client`, `miner`, and `server` programs using `go install` before running the tests!_

```bash
# Run ctest on a Mac OS X machine in non-verbose mode.
$GOPATH/bin/darwin_amd64/ctest

# Run mtest on a Linux machine in verbose mode.
$GOPATH/bin/linux_amd64/mtest -v
```

The logging messages to standard output made use of `log.Logger` and direct them to a file, as
illustrated by the code below:

```go
const (
	name = "log.txt"
	flag = os.O_RDWR | os.O_CREATE
	perm = os.FileMode(0666)
)

file, err := os.OpenFile(name, flag, perm)
if err != nil {
	return
}

LOGF := log.New(file, "", log.Lshortfile|log.Lmicroseconds)
LOGF.Println("Bees?!", "Beads.", "Gob's not on board.")
```
