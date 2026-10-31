package inference

import (
	"context"
	"reflect"
	"testing"
	"testing/synctest"
)

func TestSharedPoolAdmitsOnlyWhatFitsTheCache(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(Pool{Name: "gpu", Slots: 4, KV: KVShared, Cache: 131072})
		a := acquire(t, q, Need{Pool: "gpu", SessionID: "a", Tokens: 100_000})
		b := begin(context.Background(), q, Need{Pool: "gpu", SessionID: "b", Tokens: 100_000})
		if b.admitted() {
			t.Fatal("two calls of 100k tokens were admitted together on a 128k cache")
		}
		// Slots were free: only the cache held it back.
		if st := q.Snapshot()[0]; len(st.Running) != 1 || st.Used != 100_000 {
			t.Errorf("pool = %+v", st)
		}
		a.Release()
		l := b.get(t)
		l.Release()
	})
}

func TestSharedPoolAdmitsCallsThatFitTogether(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(Pool{Name: "gpu", Slots: 4, KV: KVShared, Cache: 131072})
		a := acquire(t, q, Need{Pool: "gpu", Tokens: 60_000})
		b := acquire(t, q, Need{Pool: "gpu", Tokens: 71_072}) // exactly the cache
		c := begin(context.Background(), q, Need{Pool: "gpu", Tokens: 1})
		if c.admitted() {
			t.Fatal("a call was admitted into a full cache")
		}
		a.Release()
		c.get(t).Release()
		b.Release()
	})
}

func TestPartitionedPoolIsLimitedBySlotsAlone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pools := Pools([]Endpoint{{Name: "llama", Slots: 2, KV: KVPartitioned, KVTokens: 1000, Window: 1000}})
		q := newQueue(pools...)
		a := acquire(t, q, Need{Pool: "llama", Tokens: 900_000})
		b := acquire(t, q, Need{Pool: "llama", Tokens: 900_000})
		a.Release()
		b.Release()
	})
}

func TestSharedPoolOfUnknownSizeIsLimitedBySlotsAlone(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pools := Pools([]Endpoint{{Name: "vllm", Slots: 2, KV: KVShared}})
		q := newQueue(pools...)
		a := acquire(t, q, Need{Pool: "vllm", Tokens: 900_000})
		b := acquire(t, q, Need{Pool: "vllm", Tokens: 900_000})
		a.Release()
		b.Release()
	})
}

func TestUnknownKVIsAdmittedAsSharedWithTheWindowAsItsSize(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		pools := Pools([]Endpoint{{Name: "llama", Slots: 2, KV: KVUnknown, Window: 131072}})
		if got := pools[0].Cache; got != 131072 {
			t.Fatalf("cache of an unknown KV = %d, want the window", got)
		}
		q := newQueue(pools...)
		a := acquire(t, q, Need{Pool: "llama", Tokens: 100_000})
		b := begin(context.Background(), q, Need{Pool: "llama", Tokens: 100_000})
		if b.admitted() {
			t.Fatal("an unknown KV admitted two calls that together overfill a window")
		}
		a.Release()
		b.get(t).Release()
	})
}

func TestUnknownKVWithNoWindowIsLimitedBySlotsAlone(t *testing.T) {
	pools := Pools([]Endpoint{{Name: "llama", Slots: 2, KV: KVUnknown}})
	if pools[0].Cache != 0 {
		t.Errorf("cache = %d, want none", pools[0].Cache)
	}
}

func TestACallLargerThanTheWholeCacheRunsAloneRatherThanWaitingForever(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(Pool{Name: "gpu", Slots: 4, KV: KVShared, Cache: 1000})

		// On an empty pool it is admitted at once.
		huge := acquire(t, q, Need{Pool: "gpu", Tokens: 5000})
		// And it is alone: nothing else fits beside it.
		small := begin(context.Background(), q, Need{Pool: "gpu", Tokens: 10})
		if small.admitted() {
			t.Fatal("a call was admitted beside one that fills more than the cache")
		}
		huge.Release()
		small.get(t).Release()

		// Behind running calls it waits for the pool to empty, and nothing
		// that arrives later overtakes it in the meantime.
		running := acquire(t, q, Need{Pool: "gpu", Tokens: 100})
		big := begin(context.Background(), q, Need{Pool: "gpu", Tokens: 5000})
		settle()
		later := begin(context.Background(), q, Need{Pool: "gpu", Tokens: 10})
		if big.admitted() || later.admitted() {
			t.Fatal("the oversize call, or one behind it, was admitted beside a running call")
		}
		running.Release()
		l := big.get(t)
		if later.admitted() {
			t.Error("a call was admitted beside the oversize one")
		}
		l.Release()
		later.get(t).Release()
	})
}

func TestASmallCallDoesNotOvertakeALargeOneWaitingForRoom(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(Pool{Name: "gpu", Slots: 4, KV: KVShared, Cache: 1000})
		running := acquire(t, q, Need{Pool: "gpu", Tokens: 600})
		large := begin(context.Background(), q, Need{Pool: "gpu", Tokens: 600})
		settle()
		small := begin(context.Background(), q, Need{Pool: "gpu", Tokens: 100})
		if small.admitted() {
			t.Fatal("a small call overtook a large one of the same priority; a stream of them would starve it")
		}
		running.Release()
		l := large.get(t)
		small.get(t).Release()
		l.Release()
	})
}

func TestAMoreUrgentSmallCallRunsBesideARunningOneWhileALargeOneWaits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newQueue(Pool{Name: "gpu", Slots: 4, KV: KVShared, Cache: 1000})
		running := acquire(t, q, Need{Pool: "gpu", Tokens: 600})
		large := begin(context.Background(), q, Need{Pool: "gpu", Priority: Background, Tokens: 600})
		settle()
		urgent := acquire(t, q, Need{Pool: "gpu", Priority: Interactive, Tokens: 100})
		urgent.Release()
		running.Release()
		large.get(t).Release()
	})
}

func TestAPoolIsTheNamedPoolNotTheProvider(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Two providers on one machine: calls to either wait for the same
		// slot.
		pools := Pools([]Endpoint{
			{Name: "llama", Pool: "5090", Slots: 1, KV: KVShared, KVTokens: 1000},
			{Name: "tabby", Pool: "5090", Slots: 1, KV: KVShared, KVTokens: 1000},
		})
		if len(pools) != 1 || pools[0].Name != "5090" {
			t.Fatalf("pools = %+v, want the one named 5090", pools)
		}
		q := newQueue(pools...)
		a := acquire(t, q, Need{Pool: "5090", Endpoint: "llama"})
		b := begin(context.Background(), q, Need{Pool: "5090", Endpoint: "tabby"})
		if b.admitted() {
			t.Fatal("two providers of one pool were run side by side on one slot")
		}
		a.Release()
		b.get(t).Release()
	})
}

func TestPoolsWithNoNameAreNamedAfterTheProvider(t *testing.T) {
	pools := Pools([]Endpoint{{Name: "a", Slots: 1}, {Name: "b", Pool: "", Slots: 1}})
	if len(pools) != 2 || pools[0].Name != "a" || pools[1].Name != "b" {
		t.Fatalf("pools = %+v", pools)
	}
}

func TestRowsOfOnePoolThatDisagreeTakeTheMostRestrictiveOfEach(t *testing.T) {
	tests := []struct {
		name string
		rows []Endpoint
		want Pool
	}{
		{
			name: "fewest slots",
			rows: []Endpoint{{Name: "a", Pool: "p", Slots: 4}, {Name: "b", Pool: "p", Slots: 2}},
			want: Pool{Name: "p", Endpoints: []string{"a", "b"}, Slots: 2},
		},
		{
			name: "smallest cache",
			rows: []Endpoint{
				{Name: "a", Pool: "p", Slots: 2, KV: KVShared, KVTokens: 262144},
				{Name: "b", Pool: "p", Slots: 2, KV: KVShared, KVTokens: 131072},
			},
			want: Pool{Name: "p", Endpoints: []string{"a", "b"}, Slots: 2, KV: KVShared, Cache: 131072},
		},
		{
			name: "a row with a size binds one without",
			rows: []Endpoint{
				{Name: "a", Pool: "p", Slots: 2, KV: KVPartitioned},
				{Name: "b", Pool: "p", Slots: 2, KV: KVShared, KVTokens: 65536},
			},
			want: Pool{Name: "p", Endpoints: []string{"a", "b"}, Slots: 2, KV: KVShared, Cache: 65536},
		},
		{
			name: "window of an unknown row against a shared size",
			rows: []Endpoint{
				{Name: "a", Pool: "p", Slots: 2, KV: KVUnknown, Window: 32768},
				{Name: "b", Pool: "p", Slots: 2, KV: KVShared, KVTokens: 131072},
			},
			want: Pool{Name: "p", Endpoints: []string{"a", "b"}, Slots: 2, KV: KVShared, Cache: 32768},
		},
		{
			name: "no row states a size",
			rows: []Endpoint{{Name: "a", Pool: "p", Slots: 3, KV: KVShared}, {Name: "b", Pool: "p", Slots: 3, KV: KVPartitioned}},
			want: Pool{Name: "p", Endpoints: []string{"a", "b"}, Slots: 3, KV: KVShared},
		},
		{
			name: "slots below one count as one",
			rows: []Endpoint{{Name: "a", Slots: 0}},
			want: Pool{Name: "a", Endpoints: []string{"a"}, Slots: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Pools(tt.rows)
			if len(got) != 1 || !reflect.DeepEqual(got[0], tt.want) {
				t.Errorf("Pools = %+v, want %+v", got, tt.want)
			}
		})
	}
}
