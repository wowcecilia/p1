package lsp

import "container/list"

type T interface{}
type UnboundedQueue interface {
	Put(T)
	Shutdown()
	Close()
}

type unboundedQueue struct {
	entrance chan T     // entrance for the unbounded buffer
	exit     chan T     // exit for the unbounded buffer
	buffer   *list.List // for a unbounded buffer
	shutdown chan T
}

func NewUnboundedQueue() (UnboundedQueue, chan T) {
	buffer := &unboundedQueue{
		entrance: make(chan T),
		exit:     make(chan T),
		buffer:   list.New(),
		shutdown: make(chan T),
	}
	go buffer.start()
	return buffer, buffer.exit
}

func (buf *unboundedQueue) Put(elem T) {
	buf.entrance <- elem
}

// buffer will hold until all elems are read out
func (buf *unboundedQueue) Close() {
	close(buf.entrance)
}

// buffer will shutdown and discard the things in the buffer
func (buf *unboundedQueue) Shutdown() {
	//buf.shutdown <- nil
	close(buf.shutdown)
}

/* Rountine: Helper function for an unbounded buffer */
func (buf *unboundedQueue) start() {
	defer close(buf.exit)

	for {
		// Make sure that the list always has values before
		// we select over the two channelbuf.
		if buf.buffer.Len() == 0 {
			v, ok := <-buf.entrance
			if !ok {
				// 'entrance' has been closed. Flush all values in the buffer and return.
				LOGV.Println("unbounded going to block")
				buf.flush()
				LOGV.Println("unbounded return")
				return
			}
			buf.buffer.PushBack(v)
		}

		select {
		case v, ok := <-buf.entrance:
			if !ok {
				// 'in' has been closed. Flush all values in the buffer and return.
				LOGV.Println("unbounded going to block")
				buf.flush()
				LOGV.Println("unbounded return")
				return
			}
			buf.buffer.PushBack(v)
		case buf.exit <- (buf.buffer.Front().Value).(T):
			buf.buffer.Remove(buf.buffer.Front())
		case <-buf.shutdown:
			defer close(buf.entrance)
			return
		}
	}
}

// Blocks until all values in the buffer have been sent through
// the 'out' channel.
func (buf *unboundedQueue) flush() {
	for e := buf.buffer.Front(); e != nil; e = e.Next() {
		buf.exit <- (e.Value).(T)
	}
}
