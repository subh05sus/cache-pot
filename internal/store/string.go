package store

import (
	"errors"
	"strconv"
	"time"
)

// ErrWrongType mirrors Redis' WRONGTYPE condition: an operation was attempted
// against a key holding a different data type.
var ErrWrongType = errors.New("WRONGTYPE Operation against a key holding the wrong kind of value")

// ErrNotInteger is returned when a string value cannot be parsed as an int64
// for INCR/DECR-style operations.
var ErrNotInteger = errors.New("ERR value is not an integer or out of range")

// asString returns the string value of e, or ErrWrongType.
func asString(e *entry) (string, error) {
	s, ok := e.val.(string)
	if !ok {
		return "", ErrWrongType
	}
	return s, nil
}

// Get returns the string at key. ok is false if the key is missing.
func (s *Store) Get(key string) (val string, ok bool, err error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, found := sh.getLive(key, s.now())
	if !found {
		return "", false, nil
	}
	str, err := asString(e)
	if err != nil {
		return "", false, err
	}
	return str, true, nil
}

// SetOptions controls SET behaviour (EX/PX/NX/XX flags).
type SetOptions struct {
	TTL   time.Duration // 0 means no expiry
	HasEx bool          // an EX/PX flag was supplied
	NX    bool          // only set if the key does not exist
	XX    bool          // only set if the key already exists
}

// Set assigns a string value to key, honouring the supplied options. It
// reports whether the write happened (NX/XX can suppress it).
func (s *Store) Set(key, val string, opts SetOptions) bool {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	_, exists := sh.getLive(key, s.now())
	if opts.NX && exists {
		return false
	}
	if opts.XX && !exists {
		return false
	}
	ne := &entry{val: val}
	if opts.HasEx {
		ne.expireAt = s.now().Add(opts.TTL)
	}
	sh.m[key] = ne
	return true
}

// GetSet sets key to val and returns the previous string value.
func (s *Store) GetSet(key, val string) (old string, hadOld bool, err error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, found := sh.getLive(key, s.now())
	if found {
		if old, err = asString(e); err != nil {
			return "", false, err
		}
		hadOld = true
	}
	sh.m[key] = &entry{val: val}
	return old, hadOld, nil
}

// Append concatenates val to the string at key (creating it if absent) and
// returns the new length.
func (s *Store) Append(key, val string) (int, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, found := sh.getLive(key, s.now())
	if !found {
		sh.m[key] = &entry{val: val}
		return len(val), nil
	}
	cur, err := asString(e)
	if err != nil {
		return 0, err
	}
	e.val = cur + val
	return len(e.val.(string)), nil
}

// IncrBy adds delta to the integer string at key (treating a missing key as 0)
// and returns the new value.
func (s *Store) IncrBy(key string, delta int64) (int64, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, found := sh.getLive(key, s.now())
	var cur int64
	if found {
		str, err := asString(e)
		if err != nil {
			return 0, err
		}
		n, err := strconv.ParseInt(str, 10, 64)
		if err != nil {
			return 0, ErrNotInteger
		}
		cur = n
	}
	cur += delta
	if found {
		e.val = strconv.FormatInt(cur, 10)
	} else {
		sh.m[key] = &entry{val: strconv.FormatInt(cur, 10)}
	}
	return cur, nil
}

// GetRange returns the substring between start and end (inclusive), with Redis-style
// negative offsets and clamping. A missing key yields an empty string.
func (s *Store) GetRange(key string, start, end int64) (string, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	e, found := sh.getLive(key, s.now())
	if !found {
		return "", nil
	}
	str, err := asString(e)
	if err != nil {
		return "", err
	}
	n := int64(len(str))
	if start < 0 {
		start += n
	}
	if end < 0 {
		end += n
	}
	if start < 0 {
		start = 0
	}
	if end < 0 {
		end = 0
	}
	if end >= n {
		end = n - 1
	}
	if start > end {
		return "", nil
	}
	return str[start : end+1], nil
}

// SetRange overwrites from offset with val, zero-padding past the current end.
// It returns the length of the string after the write.
func (s *Store) SetRange(key string, offset int, val string) (int, error) {
	sh := s.shardFor(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	var cur string
	e, found := sh.getLive(key, s.now())
	if found {
		str, err := asString(e)
		if err != nil {
			return 0, err
		}
		cur = str
	}
	if val == "" {
		return len(cur), nil
	}
	size := offset + len(val)
	if len(cur) > size {
		size = len(cur)
	}
	buf := make([]byte, size)
	copy(buf, cur)
	copy(buf[offset:], val)
	if found {
		e.val = string(buf)
	} else {
		sh.m[key] = &entry{val: string(buf)}
	}
	return size, nil
}
