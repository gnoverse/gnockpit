package location_test

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gnolang/gno/tm2/pkg/crypto"
	"modernc.org/sqlite"

	"github.com/gnoverse/gnockpit/location"
)

// storeAt is the time of the declarations the store tests put first.
var storeAt = time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

var (
	storeOpA = crypto.AddressFromPreimage([]byte("store A")).String()
	storeOpB = crypto.AddressFromPreimage([]byte("store B")).String()
)

// openStoreDB opens an empty in-memory database, closed with the test.
func openStoreDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close the test database: %v", err)
		}
	})
	return db
}

// openStore is a store on db, its rows read from db.
func openStore(t *testing.T, db *sql.DB) *location.Store {
	t.Helper()
	s, err := location.NewStore(context.Background(), db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

// declared is operator's declaration on chain at at: SG, OVH SAS.
func declared(chain, operator string, at time.Time) location.Declaration {
	return location.Declaration{
		Record: location.Record{
			Chain: chain, Operator: operator, Lat: 1.3, Lon: 103.8, Country: "SG", Provider: "OVH SAS", At: at,
		},
		Signed: "the document of " + operator + " at " + at.Format(time.RFC3339),
	}
}

// removal is operator's removal on chain at at.
func removal(chain, operator string, at time.Time) location.Declaration {
	return location.Declaration{
		Record: location.Record{Chain: chain, Operator: operator, Clear: true, At: at},
		Signed: "the removal of " + operator + " at " + at.Format(time.RFC3339),
	}
}

func put(t *testing.T, s *location.Store, d location.Declaration) {
	t.Helper()
	if err := s.Put(context.Background(), d); err != nil {
		t.Fatalf("Put(%+v): %v", d, err)
	}
}

// holds checks that s holds want as chain's declaration of want's operator.
func holds(t *testing.T, s *location.Store, want location.Declaration) {
	t.Helper()
	got, ok := s.Get(want.Chain, want.Operator)
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("Get(%s, %s) = %+v, %t; want %+v, true", want.Chain, want.Operator, got, ok, want)
	}
}

func TestStorePutGet(t *testing.T) {
	db := openStoreDB(t)
	s := openStore(t, db)
	d := declared("a", storeOpA, storeAt)
	put(t, s, d)

	for name, s := range map[string]*location.Store{"the store": s, "a store opened on its database": openStore(t, db)} {
		t.Run(name, func(t *testing.T) {
			holds(t, s, d)
			if got := s.All("a"); !reflect.DeepEqual(got, []location.Declaration{d}) {
				t.Errorf("All(a) = %+v, want [%+v]", got, d)
			}
			if got, ok := s.Get("a", storeOpB); ok {
				t.Errorf("Get(a, %s) = %+v, true; want none", storeOpB, got)
			}
			if got := s.All("b"); len(got) != 0 {
				t.Errorf("All(b) = %+v, want none", got)
			}
		})
	}
}

func TestStoreRefusesOlderOrEqual(t *testing.T) {
	db := openStoreDB(t)
	s := openStore(t, db)
	stored := declared("a", storeOpA, storeAt)
	put(t, s, stored)

	want := "a declaration as recent or more recent is stored for " + storeOpA + " (at 2026-11-26T12:00:00Z)"
	for _, c := range []struct {
		name string
		at   time.Time
	}{
		{"the same time", storeAt},
		{"a second before", storeAt.Add(-time.Second)},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := declared("a", storeOpA, c.at)
			d.Country = "FR"
			err := s.Put(context.Background(), d)
			if !errors.Is(err, location.ErrNotNewer) || err.Error() != want {
				t.Errorf("Put at %s = %v, want %q (ErrNotNewer)", c.at.Format(time.RFC3339), err, want)
			}
			holds(t, s, stored)
		})
	}

	newer := declared("a", storeOpA, storeAt.Add(time.Second))
	put(t, s, newer)
	holds(t, s, newer)
	reopened := openStore(t, db)
	holds(t, reopened, newer)
	if got := reopened.All("a"); len(got) != 1 {
		t.Errorf("All(a) after the replacement = %+v, want one declaration", got)
	}
}

func TestStoreKeepsARemoval(t *testing.T) {
	db := openStoreDB(t)
	s := openStore(t, db)
	put(t, s, declared("a", storeOpA, storeAt))
	gone := removal("a", storeOpA, storeAt.Add(time.Second))
	put(t, s, gone)

	reopened := openStore(t, db)
	for _, s := range []*location.Store{s, reopened} {
		holds(t, s, gone)
		if got := s.All("a"); !reflect.DeepEqual(got, []location.Declaration{gone}) {
			t.Errorf("All(a) = %+v, want [%+v]", got, gone)
		}
	}
	var latNull, lonNull bool
	if err := db.QueryRow(`SELECT lat IS NULL, lon IS NULL FROM location_declarations WHERE chain = ? AND operator = ?`,
		"a", storeOpA).Scan(&latNull, &lonNull); err != nil {
		t.Fatal(err)
	}
	if !latNull || !lonNull {
		t.Errorf("the removal's row: lat NULL %t, lon NULL %t; want both NULL", latNull, lonNull)
	}

	for _, c := range []struct {
		name string
		at   time.Time
	}{
		{"older than the removal", storeAt},
		{"as recent as the removal", storeAt.Add(time.Second)},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := reopened.Put(context.Background(), declared("a", storeOpA, c.at)); !errors.Is(err, location.ErrNotNewer) {
				t.Errorf("Put a location at %s after the removal = %v, want ErrNotNewer", c.at.Format(time.RFC3339), err)
			}
			holds(t, reopened, gone)
		})
	}
}

func TestStoreChainsApart(t *testing.T) {
	db := openStoreDB(t)
	s := openStore(t, db)
	onA := declared("a", storeOpA, storeAt)
	onB := declared("b", storeOpA, storeAt.Add(time.Hour))
	put(t, s, onA)
	put(t, s, onB)
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM location_declarations`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Errorf("rows = %d, want 2: one per chain", rows)
	}
	holds(t, s, onA)
	holds(t, s, onB)
	if got := s.All("a"); !reflect.DeepEqual(got, []location.Declaration{onA}) {
		t.Errorf("All(a) = %+v, want [%+v]", got, onA)
	}

	// All lists by operator, whatever the order of the Puts.
	other := declared("a", storeOpB, storeAt)
	put(t, s, other)
	want := []location.Declaration{onA, other}
	slices.SortFunc(want, func(x, y location.Declaration) int { return cmp.Compare(x.Operator, y.Operator) })
	for _, s := range []*location.Store{s, openStore(t, db)} {
		if got := s.All("a"); !reflect.DeepEqual(got, want) {
			t.Errorf("All(a) = %+v, want %+v (by operator)", got, want)
		}
	}
}

func TestStoreRoundTripsCoordinates(t *testing.T) {
	db := openStoreDB(t)
	s := openStore(t, db)
	var want []location.Declaration
	for i, c := range []struct{ lat, lon float64 }{{-0.1, 180}, {0, -180}, {90, 180}} {
		d := declared("a", crypto.AddressFromPreimage([]byte{byte(i)}).String(), storeAt)
		d.Lat, d.Lon = c.lat, c.lon
		put(t, s, d)
		want = append(want, d)
	}
	reopened := openStore(t, db)
	for _, d := range want {
		holds(t, reopened, d)
	}
}

// The first and last times an int64 of nanoseconds since 1970 holds, the
// store's range.
var (
	storeFirst = time.Date(1677, 9, 21, 0, 12, 43, 145224192, time.UTC)
	storeLast  = time.Date(2262, 4, 11, 23, 47, 16, 854775807, time.UTC)
)

func TestStoreRefusesATimeItCannotHold(t *testing.T) {
	s := openStore(t, openStoreDB(t))
	// Its time is kept in nanoseconds since 1970 in an int64: a time out of
	// its range would be stored as another.
	for _, c := range []struct {
		name string
		at   time.Time
	}{
		{"in 1600", time.Date(1600, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"a nanosecond before the range", storeFirst.Add(-time.Nanosecond)},
		{"a nanosecond after the range", storeLast.Add(time.Nanosecond)},
		{"in 2263", time.Date(2263, 1, 1, 0, 0, 0, 0, time.UTC)},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := s.Put(context.Background(), declared("a", storeOpA, c.at))
			want := "the record's time " + c.at.Format(time.RFC3339Nano) + " is out of the store's range, " +
				"1677-09-21T00:12:43.145224192Z to 2262-04-11T23:47:16.854775807Z"
			if err == nil || err.Error() != want {
				t.Errorf("Put at %s = %v, want %q", c.at.Format(time.RFC3339Nano), err, want)
			}
			if got, ok := s.Get("a", storeOpA); ok {
				t.Errorf("Get after the refused Put = %+v, want none", got)
			}
		})
	}
}

func TestStoreRoundTripsTheRangesEdges(t *testing.T) {
	db := openStoreDB(t)
	s := openStore(t, db)
	first := declared("a", storeOpA, storeFirst)
	last := declared("a", storeOpB, storeLast)
	put(t, s, first)
	put(t, s, last)
	reopened := openStore(t, db)
	holds(t, reopened, first)
	holds(t, reopened, last)
}

// storeRounds and storeWriters are how many times the concurrency tests race
// how many Puts, so a check made apart from its write loses a race in some
// round.
const (
	storeRounds  = 30
	storeWriters = 16
)

func TestStoreConcurrentPutsOfOneTime(t *testing.T) {
	for round := range storeRounds {
		s := openStore(t, openStoreDB(t))
		errs := make([]error, storeWriters)
		var wg sync.WaitGroup
		for i := range storeWriters {
			wg.Go(func() { errs[i] = s.Put(context.Background(), declared("a", storeOpA, storeAt)) })
		}
		wg.Wait()
		stored, notNewer := 0, 0
		for _, err := range errs {
			switch {
			case err == nil:
				stored++
			case errors.Is(err, location.ErrNotNewer):
				notNewer++
			default:
				t.Errorf("round %d: Put = %v, want nil or ErrNotNewer", round, err)
			}
		}
		if stored != 1 || notNewer != storeWriters-1 {
			t.Fatalf("round %d: %d Puts stored, %d refused as not newer; want 1 and %d", round, stored, notNewer, storeWriters-1)
		}
	}
}

// TestStoreConcurrentPutsKeepTheNewest races Puts of distinct times, with
// readers going through Get and All all along, so the race detector sees a
// mirror update made without its lock.
func TestStoreConcurrentPutsKeepTheNewest(t *testing.T) {
	newest := storeAt.Add((storeWriters - 1) * time.Second)
	for round := range storeRounds {
		db := openStoreDB(t)
		s := openStore(t, db)
		written := make(chan struct{})
		var readers sync.WaitGroup
		for range 2 {
			readers.Go(func() {
				for {
					select {
					case <-written:
						return
					default:
						s.Get("a", storeOpA)
						s.All("a")
					}
				}
			})
		}
		var wg sync.WaitGroup
		for i := range storeWriters {
			wg.Go(func() {
				err := s.Put(context.Background(), declared("a", storeOpA, storeAt.Add(time.Duration(i)*time.Second)))
				if err != nil && !errors.Is(err, location.ErrNotNewer) {
					t.Errorf("round %d: Put = %v, want nil or ErrNotNewer", round, err)
				}
			})
		}
		wg.Wait()
		close(written)
		readers.Wait()
		if got, _ := s.Get("a", storeOpA); !got.At.Equal(newest) {
			t.Fatalf("round %d: Get's time = %s, want the newest, %s", round, got.At, newest)
		}
		var atNs int64
		if err := db.QueryRow(`SELECT at_ns FROM location_declarations WHERE chain = ? AND operator = ?`, "a", storeOpA).Scan(&atNs); err != nil {
			t.Fatal(err)
		}
		if got := time.Unix(0, atNs).UTC(); !got.Equal(newest) {
			t.Fatalf("round %d: the row's time = %s, want the newest, %s", round, got, newest)
		}
	}
}

func TestStoreFailedWriteKeepsTheMirror(t *testing.T) {
	db := openStoreDB(t)
	s := openStore(t, db)
	first := declared("a", storeOpA, storeAt)
	put(t, s, first)
	if _, err := db.Exec(`DROP TABLE location_declarations`); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(context.Background(), declared("a", storeOpA, storeAt.Add(time.Second))); err == nil {
		t.Fatal("Put with the table dropped = nil, want its failure")
	}
	holds(t, s, first)
	if got := s.All("a"); !reflect.DeepEqual(got, []location.Declaration{first}) {
		t.Errorf("All(a) = %+v, want [%+v]", got, first)
	}
}

func TestStorePutOutlivesItsContext(t *testing.T) {
	db := openStoreDB(t)
	s := openStore(t, db)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := declared("a", storeOpA, storeAt)
	if err := s.Put(ctx, d); err != nil {
		t.Fatalf("Put with its context canceled = %v, want nil: a caller that leaves does not abort the write", err)
	}
	holds(t, s, d)
	holds(t, openStore(t, db), d)
}

// holdDatabase takes db's one connection until the test ends, so the next
// write to db waits for it.
func holdDatabase(t *testing.T, db *sql.DB) *sql.Conn {
	t.Helper()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
			t.Errorf("release the database: %v", err)
		}
	})
	return conn
}

func TestStorePutBoundsItsWaitForTheDatabase(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db := openStoreDB(t)
		s := openStore(t, db)
		holdDatabase(t, db)

		start := time.Now()
		err := s.Put(context.Background(), declared("a", storeOpA, storeAt))
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Put while the database is held = %v, want context.DeadlineExceeded", err)
		}
		if waited := time.Since(start); waited != 10*time.Second {
			t.Errorf("Put gave up after %s, want 10s", waited)
		}
		if got, ok := s.Get("a", storeOpA); ok {
			t.Errorf("Get after the failed Put = %+v, want none", got)
		}
	})
}

// TestStorePutKeepsAWriteThatCommitsLate checks that a write whose commit
// outlasts writeTimeout is reported and mirrored as stored: only the wait for
// the connection is bounded, never the write, which an interrupt would report
// failed once committed.
func TestStorePutKeepsAWriteThatCommitsLate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "store.db"))
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		t.Cleanup(func() {
			if err := db.Close(); err != nil {
				t.Errorf("close the test database: %v", err)
			}
		})
		s := openStore(t, db)
		first := declared("a", storeOpA, storeAt)
		put(t, s, first)

		// The commit hook of the database's one connection holds the next
		// commit past writeTimeout, in the bubble's time.
		slow := false
		conn, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.Raw(func(dc any) error {
			dc.(sqlite.HookRegisterer).RegisterCommitHook(func() int32 {
				if slow {
					slow = false
					time.Sleep(11 * time.Second)
				}
				return 0
			})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := conn.Close(); err != nil {
			t.Fatal(err)
		}

		slow = true
		newer := declared("a", storeOpA, storeAt.Add(time.Second))
		if err := s.Put(context.Background(), newer); err != nil {
			t.Errorf("Put of a write that commits after 11 s = %v, want nil", err)
		}
		holds(t, s, newer)
		holds(t, openStore(t, db), newer)
	})
}

func TestStoreReadsDoNotWaitOnAPut(t *testing.T) {
	db := openStoreDB(t)
	s := openStore(t, db)
	first := declared("a", storeOpA, storeAt)
	put(t, s, first)
	conn := holdDatabase(t, db)

	// The timer only bounds a failure: a Put that never reaches the
	// database, or a read that waits on the Put, never ends while the
	// database is held.
	failure := time.NewTimer(5 * time.Second)
	defer failure.Stop()
	waits := db.Stats().WaitCount
	newer := declared("a", storeOpA, storeAt.Add(time.Second))
	putDone := make(chan error, 1)
	go func() { putDone <- s.Put(context.Background(), newer) }()
	for db.Stats().WaitCount == waits {
		select {
		case err := <-putDone:
			t.Fatalf("Put = %v without waiting on the database", err)
		case <-failure.C:
			t.Fatal("Put never waited on the database")
		default:
			runtime.Gosched()
		}
	}

	read := make(chan []location.Declaration, 1)
	go func() {
		d, _ := s.Get("a", storeOpA)
		read <- append(s.All("a"), d)
	}()
	select {
	case got := <-read:
		if want := []location.Declaration{first, first}; !reflect.DeepEqual(got, want) {
			t.Errorf("All and Get while the Put waits = %+v, want the stored declaration twice", got)
		}
	case <-failure.C:
		t.Error("All and Get waited on the Put's write")
	}

	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-putDone; err != nil {
		t.Fatalf("Put once the database is free = %v", err)
	}
	holds(t, s, newer)
}
