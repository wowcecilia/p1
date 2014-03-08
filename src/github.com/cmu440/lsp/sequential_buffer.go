package lsp

// reading: might get the later ones first.. need buffer
// using acked and next
// block??
type SequentialBuffer struct {
	Buffer map[int][]byte
	head   int
	//tail int
}

func NewSequentialBuffer() *SequentialBuffer {
	rb := &SequentialBuffer{
		Buffer: make(map[int][]byte),
		head:   1,
		//tail: 1,
	}
	return rb
}

func (buf *SequentialBuffer) Put(seq int, payload []byte) {
	if seq < buf.head {
		return
	} else {
		buf.Buffer[seq] = payload
		// if seq > buf.tail {
		// buf.tail = seq
		//}
	}
}

func (buf *SequentialBuffer) GetNext() (int, []byte) {
	//if buf.head <= buf.tail {
	// buffer is empty
	//return 0, nil
	//}
	val, ok := buf.Buffer[buf.head]
	if ok {
		buf.head += 1
		delete(buf.Buffer, buf.head-1)
		return buf.head - 1, val
	} else {
		return 0, nil
	}
}
