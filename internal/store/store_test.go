package store

import (
	"testing"
	"time"
)

func TestStringSetGet(t *testing.T) {
	s := New()
	s.Set("k", "v", SetOptions{})
	got, ok, err := s.Get("k")
	if err != nil || !ok || got != "v" {
		t.Fatalf("Get = %q %v %v", got, ok, err)
	}
}

func TestSetNX(t *testing.T) {
	s := New()
	if !s.Set("k", "v1", SetOptions{NX: true}) {
		t.Fatal("first NX set should succeed")
	}
	if s.Set("k", "v2", SetOptions{NX: true}) {
		t.Fatal("second NX set should fail")
	}
	got, _, _ := s.Get("k")
	if got != "v1" {
		t.Fatalf("got %q", got)
	}
}

func TestIncrBy(t *testing.T) {
	s := New()
	n, err := s.IncrBy("c", 5)
	if err != nil || n != 5 {
		t.Fatalf("IncrBy = %d %v", n, err)
	}
	n, _ = s.IncrBy("c", -2)
	if n != 3 {
		t.Fatalf("got %d", n)
	}
}

func TestWrongType(t *testing.T) {
	s := New()
	s.LPush("l", "a")
	if _, _, err := s.Get("l"); err != ErrWrongType {
		t.Fatalf("want ErrWrongType, got %v", err)
	}
}

func TestExpiryLazy(t *testing.T) {
	s := New()
	cur := time.Unix(1000, 0)
	s.now = func() time.Time { return cur }
	s.Set("k", "v", SetOptions{})
	s.Expire("k", time.Second)
	cur = cur.Add(2 * time.Second)
	if _, ok, _ := s.Get("k"); ok {
		t.Fatal("key should have lazily expired")
	}
}

func TestSnapshotRoundtrip(t *testing.T) {
	s := New()
	s.Set("str", "hello", SetOptions{})
	s.HSet("h", map[string]string{"f": "v"})
	s.RPush("l", "a", "b")
	s.SAdd("set", "x", "y")
	s.ZAdd("z", []ZMember{{Member: "m", Score: 1.5}})
	s.VSet("vec", "id1", []float32{1, 2, 3}, "meta")

	recs := s.Export()
	s2 := New()
	s2.Import(recs)

	if v, _, _ := s2.Get("str"); v != "hello" {
		t.Fatalf("str = %q", v)
	}
	if v, _, _ := s2.HGet("h", "f"); v != "v" {
		t.Fatalf("hash = %q", v)
	}
	if n, _ := s2.LLen("l"); n != 2 {
		t.Fatalf("list len = %d", n)
	}
	if n, _ := s2.SCard("set"); n != 2 {
		t.Fatalf("set card = %d", n)
	}
	if sc, ok, _ := s2.ZScore("z", "m"); !ok || sc != 1.5 {
		t.Fatalf("zscore = %f %v", sc, ok)
	}
	if n, _ := s2.VCard("vec"); n != 1 {
		t.Fatalf("vcard = %d", n)
	}
}
