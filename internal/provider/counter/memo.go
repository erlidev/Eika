package counter

// memoSize bounds one memo. A session's prefix grows by a few entries a call,
// so this is hundreds of calls of history, and an entry is a hash and a
// number.
const memoSize = 512

// memo remembers token counts by the hash of what was counted, forgetting the
// oldest when full.
type memo struct {
	counts map[[32]byte]int
	order  [][32]byte
}

func newMemo() *memo { return &memo{counts: map[[32]byte]int{}} }

func (m *memo) get(key [32]byte) (int, bool) {
	n, ok := m.counts[key]
	return n, ok
}

func (m *memo) put(key [32]byte, n int) {
	if _, ok := m.counts[key]; !ok {
		if len(m.order) >= memoSize {
			delete(m.counts, m.order[0])
			m.order = m.order[1:]
		}
		m.order = append(m.order, key)
	}
	m.counts[key] = n
}
