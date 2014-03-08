// Contains the implementation of a LSP server.

package lsp

import (
	"encoding/json"
	"errors"
	"github.com/cmu440/lspnet"
	"log"
	//     	"os"
	"io/ioutil"
	"strconv"
	"time"
)

var LOGV = log.New(ioutil.Discard, "VERBOSE ", log.Lmicroseconds|log.Lshortfile)

//var LOGV = log.New(os.Stdout, "VERBOSE ", log.Lmicroseconds|log.Lshortfile)

type wrapper struct {
	payload []byte
	connID  int
}
type wrapper2 struct {
	addr  *lspnet.UDPAddr
	bytes []byte
}

type server struct {
	// general fields
	clientsMap        map[int]*client_info // map for active clients
	closingClientsMap map[int]*client_info // map for closing clients
	closing           bool                 // state of the server
	uConn             *lspnet.UDPConn      // listening for UDP connection
	nextConnId        int                  // the connection ID for next client
	params            *Params              // params of the server

	// rountine: handleBuffer
	readingBuffer    UnboundedQueue // it's a interface
	readingBufferOut chan T         // T is an empty interface declared in lsp

	// routine: ticker
	// ticker -> master
	ticker *time.Ticker // channel for the epoch firer

	// routine: inListener
	// inListener -> master (only one message one time)
	inMessage chan wrapper2

	// closeConn()/close() -> master
	closeChan    chan int  // when closeConn and close is put on this channel
	closeOkChan  chan bool // master handler tells closeConn() whether clients exist
	closeMessage chan bool // masterHandler sent message to close()

	// for Write() -> master
	askingChan    chan int // write ask if a client is alive
	answeringChan chan chan wrapper
	outMessage    chan wrapper
}

type client_info struct { // all fields in this struct handled by masterHandler
	connID        int               // connID for this client
	recentRecv    []int             // keep a queue for recent reveived message, resend ack after an epoch, handle by master
	addr          *lspnet.UDPAddr   // address for this client
	readingBuffer *SequentialBuffer // cache in case messages not be in order
	writingBuffer *SlidingWindow    // cache the message until the message could be sent
	inactiveEpoch int               // number inactive epoch
}

// NewServer creates, initiates, and returns a new server. This function should
// NOT block. Instead, it should spawn one or more goroutines (to handle things
// like accepting incoming client connections, triggering epoch events at
// fixed intervals, synchronizing events using a for-select loop like you saw in
// project 0, etc.) and immediately return. It should return a non-nil error if
// there was an error resolving or listening on the specified port number.
func NewServer(port int, params *Params) (Server, error) {
	addr, err := lspnet.ResolveUDPAddr("udp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return nil, err
	}

	uconn, err := lspnet.ListenUDP("udp", addr)
	if err != nil {
		return nil, err
	}

	c := time.NewTicker(time.Duration(params.EpochMillis) * time.Millisecond) // set a new ticker
	buf, bufOut := NewUnboundedQueue()
	s := &server{
		clientsMap:        make(map[int]*client_info),
		closingClientsMap: make(map[int]*client_info),
		closing:           false,
		uConn:             uconn,
		nextConnId:        1,
		params:            params,
		readingBuffer:     buf,
		readingBufferOut:  bufOut,
		ticker:            c,
		inMessage:         make(chan wrapper2),
		closeChan:         make(chan int),
		closeOkChan:       make(chan bool),
		closeMessage:      make(chan bool),
		askingChan:        make(chan int),
		answeringChan:     make(chan chan wrapper),
		outMessage:        make(chan wrapper),
	}
	go s.masterHandler()
	go s.incomeListener()
	return s, nil
}

/* api for application to read */
// server closed, return error. uhhh... but no read after close!
func (s *server) Read() (int, []byte, error) {
	raw, ok := <-s.readingBufferOut // block until ready. channel close after Close()
	if !ok {
		return 0, nil, errors.New("The server has been closed")
	}
	msg, _ := raw.(Message)
	if msg.Payload == nil { // some client is dead or closed!
		if msg.ConnID < 0 {
			LOGV.Println("Server read out timeout")
			return -msg.ConnID, nil, errors.New("Client timeout. (ConnID: " + strconv.Itoa(-msg.ConnID) + ")")
		} else {
			LOGV.Println("Server read out closed")
			return msg.ConnID, nil, errors.New("Client closed. (ConnID: " + strconv.Itoa(msg.ConnID) + ")")
		}
	} else {
		LOGV.Println("Server read out from " + strconv.Itoa(msg.ConnID) + ": " + string(msg.Payload[:]))
		return msg.ConnID, msg.Payload, nil
	}
}

/* return error if connection lost */
func (s *server) Write(connID int, payload []byte) error {
	LOGV.Println("Server want to write to " + strconv.Itoa(connID) + " " + string(payload[:]))
	if connID <= 0 {
		return errors.New("Error at Write: No such client")
	}
	s.askingChan <- connID
	sendingChan := <-s.answeringChan // get the channel to write on

	if sendingChan == nil {
		return errors.New("Error at Write: Connection has been lost")
	}
	sendingChan <- wrapper{payload, connID}
	return nil
}

/* Close the connection. Don't block. Return error if no such client*/
func (s *server) CloseConn(connID int) error {
	if connID <= 0 {
		return errors.New("ConnID not valid")
	}
	s.closeChan <- connID
	ok := <-s.closeOkChan
	if ok {
		return nil
	} else {
		return errors.New("ConnID " + strconv.Itoa(connID) + ": No such client or the client to be closed")
	}
}

/* api for application to shutdown the server */
// return error if clients lost
func (s *server) Close() error {
	// stop reading, should block and terminate all go rountines
	// mobe all the clients to the closing clients... then wait for them
	LOGV.Println("Server wants to terminate")
	s.closeChan <- 0

	num := 0 // number of lost clients
	for ok := range s.closeMessage {
		if ok {
			break
		} else {
			num += 1
		}
	} // masterHandler should be stopped

	s.ticker.Stop() // should stop ticker routine
	// s.readingBuffer.Shutdown()
	// reading Buffer is shut down when called
	s.uConn.Close() // should stop incomeListener
	if num == 0 {
		return nil
	} else {
		return errors.New(strconv.Itoa(num) + "  clients died during close()")
	}
}

/* Routine: keep reading from the network */
func (s *server) incomeListener() error {
	for {
		bytes := make([]byte, 2000)
		length, addr, err := s.uConn.ReadFromUDP(bytes) // bytes, addr
		// return when the connection is closed
		if err != nil {
			close(s.inMessage)
			return err // the server is shutdown
		}
		LOGV.Println("Server read from network: " + string(bytes[:]))
		s.inMessage <- wrapper2{addr, bytes[:length]}
	}
}

/* Routine: master handler of the server */
func (s *server) masterHandler() {
	for {
		select {
		// don't use channel in the same routine
		case <-s.ticker.C:
			for connID, client := range s.clientsMap { // TODO also for closing client!
				client.inactiveEpoch += 1
				if client.inactiveEpoch > s.params.EpochLimit {
					s.readingBuffer.Put(*NewData(-connID, -1, nil)) // notice Read() about the death of client T__T
					delete(s.clientsMap, connID)                    // TODO clean up!
				} else {
					for seq := client.writingBuffer.Acked + 1; seq < client.writingBuffer.NextSeq; seq += 1 {

						payload, ok := client.writingBuffer.Payload[seq]
						if ok {
							//  	for seq, payload := range client.writingBuffer.Payload { // sent but not acknowledged yet
							//		if seq > client.writingBuffer.Acked && seq < client.writingBuffer.NextSeq {
							bytes, err := json.Marshal(NewData(connID, seq, payload))
							if err != nil {
								continue
							}
							s.uConn.WriteToUDP(bytes, client.addr)
							LOGV.Println("Server rewrite data to network:" + string(bytes[:]))
						}
					}
					for _, seq := range client.recentRecv {
						bytes, err := json.Marshal(NewAck(connID, seq))
						if err != nil {
							continue
						}
						s.uConn.WriteToUDP(bytes, client.addr)
						LOGV.Println("Server rewrite ACK to network: " + string(bytes[:]))
					}
				}
			}
			for connID, client := range s.closingClientsMap {
				client.inactiveEpoch += 1
				if client.inactiveEpoch > s.params.EpochLimit {
					if s.closing {
						s.closeMessage <- false // notice close a client's lost
					} else {
						s.readingBuffer.Put(*NewData(-connID, -1, nil))
					}
					delete(s.closingClientsMap, connID)
				} else {
					for seq := client.writingBuffer.Acked + 1; seq < client.writingBuffer.NextSeq; seq += 1 {

						payload, ok := client.writingBuffer.Payload[seq]
						if ok {
							//  	for seq, payload := range client.writingBuffer.Payload { // sent but not acknowledged yet
							//		if seq > client.writingBuffer.Acked && seq < client.writingBuffer.NextSeq {
							bytes, err := json.Marshal(NewData(connID, seq, payload))
							if err != nil {
								continue
							}
							s.uConn.WriteToUDP(bytes, client.addr)
							LOGV.Println("Server resend sliding window to network(closing): " + string(bytes[:]))
						}
					}
					for _, seq := range client.recentRecv {
						bytes, err := json.Marshal(NewAck(connID, seq))
						if err != nil {
							continue
						}
						s.uConn.WriteToUDP(bytes, client.addr)
						LOGV.Println("Server resend ACK to network(closing): " + string(bytes[:]))
					}
				}
			}
			if s.closing && len(s.closingClientsMap) == 0 {
				s.closeMessage <- true
				return
			}

		case connID := <-s.closeChan:
			if connID == 0 { // close all clients
				s.closing = true
				s.readingBuffer.Shutdown()
				for connID, client := range s.clientsMap {
					//s.readingBuffer.Put(NewData(connID, 0, nil)) since no Read() will be called
					if !client.writingBuffer.IsEmpty() {
						s.closingClientsMap[connID] = client
					}
					delete(s.clientsMap, connID)
				}
				if len(s.closingClientsMap) == 0 {
					s.closeMessage <- true
					return
				}
			} else {
				client, ok := s.clientsMap[connID]
				if ok {
					s.readingBuffer.Put(*NewData(connID, 0, nil)) // notify Read() the client's closed
					if !client.writingBuffer.IsEmpty() {
						s.closingClientsMap[connID] = client
					}
					delete(s.clientsMap, connID)
					s.closeOkChan <- true
				} else {
					s.closeOkChan <- false
				}
			}

			/* correspondence with Write() */
		case connID := <-s.askingChan:
			_, ok := s.clientsMap[connID]
			if ok {
				s.answeringChan <- s.outMessage
			} else {
				s.answeringChan <- nil
			}
		case wmsg := <-s.outMessage: // wmsg is a wrapper...
			// uhh.. trouble if write and close are called simultaneously
			cl, ok := s.clientsMap[wmsg.connID]
			if ok {
				cl.writingBuffer.Put(wmsg.payload)
				seq, payload := cl.writingBuffer.GetNext()
				for payload != nil { // check the sliding window, see if the message could be sent
					sent_bytes, _ := json.Marshal(NewData(wmsg.connID, seq, payload))
					s.uConn.WriteToUDP(sent_bytes, cl.addr) // not block
					LOGV.Println("Server write to network: " + string(sent_bytes[:]))
					seq, payload = cl.writingBuffer.GetNext()
				} // since ack might not come in order, use for to send all that could send
			}

			/* handle income message from listener*/
		case w2 := <-s.inMessage:
			addr := w2.addr
			bytes := w2.bytes
			var msg Message
			err := json.Unmarshal(bytes, &msg)
			if err != nil {
				break
			}

			switch msg.Type {
			case MsgConnect:
				if s.closing {
					break // ignore new connection when closing
				}
				client := client_info{
					connID:        s.nextConnId,
					recentRecv:    []int{},
					addr:          addr,
					readingBuffer: NewSequentialBuffer(),
					writingBuffer: NewSlidingWindow(s.params.WindowSize),
					inactiveEpoch: 0,
				}
				s.clientsMap[s.nextConnId] = &client
				client.recentRecv = append(client.recentRecv[:], 0)
				sent_bytes, _ := json.Marshal(NewAck(s.nextConnId, 0))
				s.uConn.WriteToUDP(sent_bytes, addr) // send an ack
				LOGV.Println("Server write to network: " + string(bytes[:]))
				s.nextConnId += 1

			case MsgData:
				client, ok := s.clientsMap[msg.ConnID]

				if !ok { // include the case when the server is closing
					client, ok := s.closingClientsMap[msg.ConnID]
					if ok {
						client.inactiveEpoch = 0 // this client is not dead, but won't take its new data
					}
					break
				}

				client.inactiveEpoch = 0

				sent_bytes, _ := json.Marshal(NewAck(msg.ConnID, msg.SeqNum))
				s.uConn.WriteToUDP(sent_bytes, client.addr) // sending ack
				LOGV.Println("Server write ACK:" + string(bytes[:]))
				client.readingBuffer.Put(msg.SeqNum, msg.Payload)

				// check if the message could be read. The unbounded buffer does not block
				seq, nextToRead := client.readingBuffer.GetNext()
				for nextToRead != nil {
					s.readingBuffer.Put(*NewData(msg.ConnID, seq, nextToRead))
					seq, nextToRead = client.readingBuffer.GetNext()
				}

				// for a robust connection
				if len(client.recentRecv) == 1 && client.recentRecv[0] == 0 {
					// new client just connected
					client.recentRecv[0] = msg.SeqNum
				} else if len(client.recentRecv) >= s.params.WindowSize {
					// queue is full
					client.recentRecv = append(client.recentRecv[1:], msg.SeqNum)
				} else {
					// queue is not full
					client.recentRecv = append(client.recentRecv[:], msg.SeqNum)
				}

			case MsgAck: // last message must be ack if no clients lost
				client, ok := s.clientsMap[msg.ConnID]
				ok2 := false
				if !ok {
					client, ok2 = s.closingClientsMap[msg.ConnID]
					if !ok2 {
						break
					}
				}
				LOGV.Println("server get ack from " + strconv.Itoa(msg.ConnID) + " seq: " + strconv.Itoa(msg.SeqNum))
				client.inactiveEpoch = 0
				client.writingBuffer.Acknowledge(msg.SeqNum)
				seq, nextToWrite := client.writingBuffer.GetNext()
				// send out all that could be sent
				for nextToWrite != nil { // write shouldn't block
					sentBytes, _ := json.Marshal(NewData(msg.ConnID, seq, nextToWrite))
					s.uConn.WriteToUDP(sentBytes, client.addr)
					LOGV.Println("Server write to network: " + string(bytes[:]))
					seq, nextToWrite = client.writingBuffer.GetNext()
				}
				if ok2 { // a closing client, check if the clients has finished the task
					LOGV.Println("Server is closing")
					if client.writingBuffer.IsEmpty() {
						delete(s.closingClientsMap, msg.ConnID)
						if s.closing && len(s.closingClientsMap) == 0 {
							s.closeMessage <- true
							close(s.closeMessage)
							return
						}
					}
				}
			}
		}
	}
}
