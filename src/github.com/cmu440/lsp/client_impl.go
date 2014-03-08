// Contains the implementation of a LSP client.

package lsp

import (
	"encoding/json"
	"errors"
	"github.com/cmu440/lspnet"
	"time"
	//      "log"
	//     "os"
	// "io/ioutil"
)

//var LOGV = log.New(ioutil.Discard, "VERBOSE ", log.Lmicroseconds|log.Lshortfile)
//var LOGV = log.New(os.Stdout, "VERBOSE ", log.Lmicroseconds|log.Lshortfile)

type client struct {
	// TODO: implement this!
	connID        int
	uConn         *lspnet.UDPConn
	writingBuffer *SlidingWindow
	writingChan   chan []byte // Write() -> masterHandler
	writingOk     chan bool   // masterHandler -> Write()

	ticker *time.Ticker
	params *Params

	cachingBuffer *SequentialBuffer // in case message are not in order
	recentRecv    []int             // for resending ack

	inMessage        chan []byte // inListener -> master
	readingBuffer    UnboundedQueue
	readingBufferOut chan T

	inactiveEpoch int
	ready         chan int // one time channel to get the connection ID when starting
	starting      bool     // see if connection could be established
	closing       bool     // client ignore the income message after closing is set

	startingAsk    chan bool
	startingAnswer chan bool

	closingSignal chan bool // Close() -> masterHandler if the messag should be read out
	closingOk     chan bool // true if well done and false if connection lost
}

// NewClient creates, initiates, and returns a new client. This function
// should return after a connection with the server has been established
// (i.e., the client has received an Ack message from the server in response
// to its connection request), and should return a non-nil error if a
// connection could not be made (i.e., if after K epochs, the client still
// hasn't received an Ack message from the server in response to its K
// connection requests).
//
// hostport is a colon-separated string identifying the server's host address
// and port number (i.e., "localhost:9999").
func NewClient(hostport string, params *Params) (Client, error) {
	raddr, err := lspnet.ResolveUDPAddr("udp", hostport)
	if err != nil {
		return nil, err
	}
	uConn, err := lspnet.DialUDP("udp", nil, raddr) // use Write() later!!
	if err != nil {
		return nil, err
	}

	// set up client
	c := time.NewTicker(time.Duration(params.EpochMillis) * time.Millisecond) // set a new ticker
	buf, bufOut := NewUnboundedQueue()                                        // new routine
	client := &client{
		connID:           -1, // modify when connected
		uConn:            uConn,
		writingBuffer:    NewSlidingWindow(params.WindowSize),
		writingChan:      make(chan []byte),
		writingOk:        make(chan bool),
		ticker:           c,
		params:           params,
		cachingBuffer:    NewSequentialBuffer(),
		recentRecv:       []int{},
		inMessage:        make(chan []byte),
		readingBuffer:    buf,
		readingBufferOut: bufOut,
		inactiveEpoch:    0,
		ready:            make(chan int),
		starting:         true,
		closing:          false,
		closingSignal:    make(chan bool),
		closingOk:        make(chan bool),
		startingAsk:      make(chan bool),
		startingAnswer:   make(chan bool),
	}
	go client.masterHandler()
	go client.incomeListener()

	// try connect now
	bytes, err := json.Marshal(NewConnect())
	if err != nil {
		return nil, err
	} else {
		uConn.Write(bytes)
	}

	// wait the ack from the server
	connID, ok := <-client.ready
	if !ok { // time out
		return nil, errors.New("Cannot establish the connection: timeout")
	}
	client.connID = connID // no race will happen. ConnID() is called only when connection is succesfully established. Master handler will never changed outside this start process
	return client, nil
}

func (c *client) ConnID() int {
	return c.connID
}

// return error if connection closed or connection lost & no pending messages to be read
func (c *client) Read() ([]byte, error) {
	raw, ok := <-c.readingBufferOut
	if ok {
		bytes := raw.([]byte)
		LOGV.Println("Client read out:" + string(bytes[:]))
		return bytes, nil
	} else {
		return nil, errors.New("Connection has been lost")
	}
}

// return error if connection lost
func (c *client) Write(payload []byte) error {
	LOGV.Println("Client want to write:" + string(payload[:]))
	c.writingChan <- payload // see if the connection lost
	_, ok := <-c.writingOk
	if !ok {
		_ = <-c.writingChan // prevent blocking if another Write() is called
		return errors.New("Connections has been lost")
	} else {
		return nil
	}
}

func (c *client) Close() error {
	c.closingSignal <- true
	res, ok := <-c.closingOk // block here if not closed
	if !ok {
		//if ok close(c.inMessage)
		_ = <-c.closingSignal // prevent blocking on closing signal
		return errors.New("Connection has been lost")
	}

	if res {
		return nil
	} else {
		return errors.New("Connection lost during closing")
	}
}

func (c *client) incomeListener() error {
	for {
		bytes := make([]byte, 2000)
		length, err := c.uConn.Read(bytes)
		if err != nil {
			c.startingAsk <- true
			starting := <-c.startingAnswer
			if !starting {
				LOGV.Println("Client: income listener is closed")
				return err
			}
		} else {
			c.inMessage <- bytes[:length]
			LOGV.Println("Client read from network: " + string(bytes[:]))
		}
	}
}

func (c *client) masterHandler() error {
	for {
		select {
		case <-c.ticker.C:
			c.inactiveEpoch += 1
			if c.inactiveEpoch > c.params.EpochLimit {
				/* stop go routines */
				defer c.ticker.Stop() // stop ticker routine
				defer c.uConn.Close() // stop inListener routine

				if c.starting {
					LOGV.Println("closing ready")
					close(c.ready) // notify start()
				}
				if !c.closing {
					/*close channels to prevent wrong result to Close, Write and Read*/
					LOGV.Println("closing writingOk")
					close(c.writingOk) // Write will know the clients has died
					LOGV.Println("closing readingBuffer")
					c.readingBuffer.Close()
				} else {
					c.closingOk <- false
				}
				LOGV.Println("closing closingOK")
				close(c.closingOk)
				LOGV.Println("closing uConn")
				LOGV.Println("closing ticker")
				return nil
			} else { // resend messages in sliding window and acks recent received
				if c.starting {
					// resend connect message
					bytes, err := json.Marshal(NewConnect())
					if err != nil {
						return err
					} else {
						c.uConn.Write(bytes)
						LOGV.Println("Client resent connect to network: " + string(bytes[:]))
					}
				} else {
					// resend data in sliding window
					// for seq, payload := range c.writingBuffer.Payload { // sent but not acknowledged yet
					//	if seq > c.writingBuffer.Acked && seq < c.writingBuffer.NextSeq {
					for seq := c.writingBuffer.Acked + 1; seq < c.writingBuffer.NextSeq; seq += 1 {
						payload, ok := c.writingBuffer.Payload[seq]
						if ok {
							bytes, err := json.Marshal(NewData(c.connID, seq, payload))
							if err != nil {
								continue
							} else {
								c.uConn.Write(bytes)
								LOGV.Println("Client resend sliding window to network: " + string(bytes[:]))
							}
						}
					}
					// resend ack in recent recv
					for _, seq := range c.recentRecv {
						bytes, err := json.Marshal(NewAck(c.connID, seq))
						if err != nil {
							continue
						} else {
							c.uConn.Write(bytes)
							LOGV.Println("Client resend ack to network:" + string(bytes[:]))
						}
					}
				}
			}
		case <-c.startingAsk:
			c.startingAnswer <- c.starting
		case <-c.closingSignal:
			LOGV.Println("closing writingOk")
			close(c.writingOk)
			LOGV.Println("closing readingBuffer")
			c.readingBuffer.Close()
			c.closing = true // no read will be done
		case bytes := <-c.writingChan:
			c.writingBuffer.Put(bytes)
			seq, payload := c.writingBuffer.GetNext()
			for payload != nil { // check the sliding window, see if the message could be sent
				sent_bytes, _ := json.Marshal(NewData(c.connID, seq, payload))
				c.uConn.Write(sent_bytes) // not block
				LOGV.Println("Client write to network: " + string(bytes[:]))
				seq, payload = c.writingBuffer.GetNext()
			}
			c.writingOk <- true

		case bytes := <-c.inMessage:
			c.inactiveEpoch = 0 // clear the epoch
			var msg Message
			err := json.Unmarshal(bytes, &msg)
			if err != nil {
				break
			}
			if c.starting {
				c.ready <- msg.ConnID // ack
				LOGV.Println("closing ready")
				close(c.ready)
				c.starting = false
				c.recentRecv = append(c.recentRecv[:], 0) // resend ack after connected
				break
			}
			switch msg.Type {
			case MsgAck:
				c.writingBuffer.Acknowledge(msg.SeqNum)
				seq, nextToWrite := c.writingBuffer.GetNext()
				// after some message get acked, it's possible that some messages could be sent out
				for nextToWrite != nil {
					sentBytes, _ := json.Marshal(NewData(c.connID, seq, nextToWrite))
					c.uConn.Write(sentBytes)
					LOGV.Println("Client write ACK to network:" + string(bytes[:]))
					seq, nextToWrite = c.writingBuffer.GetNext()
				}

				if c.closing && c.writingBuffer.IsEmpty() {
					// see if the all done
					//         close(c.inMessage) // Read will know the connection lost
					LOGV.Println("closing ticker")
					c.ticker.Stop() // shutdown ticker
					LOGV.Println("closing conn")
					c.uConn.Close() // shutdown inListener
					//      LOGV.Println("closing buffer")
					//	c.readingBuffer.Close()
					c.closingOk <- true // all messages are sent out
					return nil
				}
			case MsgData:
				if c.closing { // ignore the message if Close() is called
					break
				}
				// put the new one into the cache and see if anything could be read out
				c.cachingBuffer.Put(msg.SeqNum, msg.Payload)
				_, nextToRead := c.cachingBuffer.GetNext()
				for nextToRead != nil {
					c.readingBuffer.Put(nextToRead)
					_, nextToRead = c.cachingBuffer.GetNext()
				}

				// send ack to server
				sent_bytes, _ := json.Marshal(NewAck(c.connID, msg.SeqNum))
				c.uConn.Write(sent_bytes) // sending ack
				LOGV.Println("Client write ACK:" + string(sent_bytes[:]))

				// update recent recv
				if len(c.recentRecv) == 1 && c.recentRecv[0] == 0 {
					// for newly connected clients
					c.recentRecv[0] = msg.SeqNum
				} else if len(c.recentRecv) >= c.params.WindowSize {
					c.recentRecv = append(c.recentRecv[1:], msg.SeqNum)
				} else { // queue is not full
					c.recentRecv = append(c.recentRecv[:], msg.SeqNum)
				}

				LOGV.Println(c.recentRecv)
			}
		}
	}
}
