package lsp

// writing using slicing window
type SlidingWindow struct {
	window_size int
	Payload     map[int][]byte // key is sequence number and value is
	Acked       int            // already acknowledged
	NextSeq     int            // next to send
	tail        int            // next to write. if seq equals tail it is not valid
}

func (sw *SlidingWindow) IsEmpty() bool {
	LOGV.Print("  sliding window from ")
	LOGV.Print(sw.Acked + 1)
	LOGV.Print(" to ")
	LOGV.Println(sw.tail)
	return sw.tail == sw.Acked+1
}

func NewSlidingWindow(window_size int) *SlidingWindow {
	sw := &SlidingWindow{
		window_size: window_size,
		Payload:     make(map[int][]byte),
		Acked:       0,
		NextSeq:     1,
		tail:        1,
	}
	return sw
}

func (w *SlidingWindow) Put(payload []byte) {
	w.Payload[w.tail] = payload
	w.tail += 1
	// should call getNextToSend later
}

func (w *SlidingWindow) Acknowledge(seq int) {
	delete(w.Payload, seq)
	if seq == w.Acked+1 {
		w.Acked = seq
		for w.Acked+1 < w.NextSeq {
			_, ok := w.Payload[w.Acked+1]
			if !ok { // aleady been acked
				w.Acked += 1
			} else {
				return
			}
		}
	}
} // should call getNextToSend later

func (w *SlidingWindow) GetNext() (int, []byte) {
	if w.NextSeq < w.tail && w.NextSeq-w.Acked <= w.window_size {
		w.NextSeq += 1
		return w.NextSeq - 1, w.Payload[w.NextSeq-1]
	}
	return 0, nil
}
