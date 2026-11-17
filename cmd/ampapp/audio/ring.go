package audio

import "sync/atomic"

// Ring é uma fila sem trava de um produtor (thread de áudio) e um consumidor (GUI).
// Se a GUI não ler a tempo, Push descarta o item em vez de bloquear o áudio.
type Ring[T any] struct {
	buf  []T
	mask uint64
	w, r atomic.Uint64
}

// NewRing cria uma fila com capacidade arredondada para potência de 2.
func NewRing[T any](capacity int) *Ring[T] {
	n := 1
	for n < capacity {
		n <<= 1
	}
	return &Ring[T]{buf: make([]T, n), mask: uint64(n - 1)}
}

// Push insere um item (produtor). Devolve false se a fila estava cheia.
func (q *Ring[T]) Push(v T) bool {
	w := q.w.Load()
	if w-q.r.Load() > q.mask {
		return false
	}
	q.buf[w&q.mask] = v
	q.w.Store(w + 1)
	return true
}

// Pop remove um item (consumidor).
func (q *Ring[T]) Pop() (T, bool) {
	var zero T
	r := q.r.Load()
	if r == q.w.Load() {
		return zero, false
	}
	v := q.buf[r&q.mask]
	q.r.Store(r + 1)
	return v, true
}
