package lite3

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
)

func TestBasic(t *testing.T) {
	b := New(nil)
	o := b.SetRootObject()

	o.SetBool("foo", true)
	if val := o.Bool("foo"); val != true {
		t.Errorf(`Object.Bool("foo") = %v; want true`, val)
	}

	a := b.SetRootArray()
	a.SetString(a.Len(), "hello")
	a.SetString(a.Len(), "world")
	if val := a.String(0); val != "hello" {
		t.Errorf(`Index(0).String() = %q; want "hello"`, val)
	}
	if val := a.String(1); val != "world" {
		t.Errorf(`Index(1).String() = %q; want "world"`, val)
	}
}

func TestIter(t *testing.T) {
	b := New(nil)

	o := b.SetRootObject()
	o.SetInt("lap", 55)
	o.SetFloat64("time_sec", 88.427)
	o.SetString("username", "jdoe")

	it := o.Iter()
	e := it.Next()
	if err := it.Err(); err != nil {
		t.Fatalf("Iter Next error: %v", err)
	} else if e.Key != "lap" || e.Value.Type != TypeInt {
		t.Errorf("Iter Next = (%q, %v); want (\"lap\", TypeInt)", e.Key, e.Value.Type)
	} else if val := e.Value.Int(); val != 55 {
		t.Errorf("Iter Next lap value = %d; want 55", val)
	}
	e = it.Next()
	if err := it.Err(); err != nil {
		t.Fatalf("Iter Next error: %v", err)
	} else if e.Key != "time_sec" || e.Value.Type != TypeFloat {
		t.Errorf("Iter Next = (%q, %v); want (\"time_sec\", TypeFloat)", e.Key, e.Value.Type)
	} else if val := e.Value.Float64(); val != 88.427 {
		t.Errorf("Iter Next time_sec value = %f; want %f", val, 88.427)
	}
	e = it.Next()
	if err := it.Err(); err != nil {
		t.Fatalf("Iter Next error: %v", err)
	} else if e.Key != "username" || e.Value.Type != TypeString {
		t.Errorf("Iter Next = (%q, %v); want (\"username\", TypeString)", e.Key, e.Value.Type)
	} else if e.Value.String() != "jdoe" {
		t.Errorf("Iter Next username value = %q; want \"jdoe\"", e.Value.String())
	}
	if it.Next() != nil {
		t.Fatalf("Expected Iter Next to return nil at end of buffer")
	} else if err := it.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestJSON(t *testing.T) {
	b := New(nil)
	o := b.SetRootObject()
	o.SetString("event", "lap_complete")
	o.SetInt("lap", 56)
	o.SetFloat64("time_sec", 88.427)

	js, _ := json.Marshal(b)
	if string(js) != `{"lap":56,"event":"lap_complete","time_sec":88.427}` {
		t.Errorf("Unexpected JSON: %s", js)
	}

	b2 := New(nil)
	if err := json.Unmarshal(js, b2); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	o2 := b2.Root().(Object)
	if lap := o2.Int("lap"); lap != 56 {
		t.Errorf("Unmarshaled lap = %d; want 56", lap)
	}
	if event := o2.String("event"); event != "lap_complete" {
		t.Errorf("Unmarshaled event = %q; want \"lap_complete\"", event)
	}
	if timeSec := o2.Float64("time_sec"); timeSec != 88.427 {
		t.Errorf("Unmarshaled time_sec = %f; want 88.427", timeSec)
	}
}

func TestJohnDoe(t *testing.T) {
	b := New(make([]byte, 0, 2048))
	o := b.SetRootObject()

	o.SetInt("user_id", 12345)
	o.SetString("username", "jdoe")
	o.SetString("email_address", "jdoe@example.com")
	o.SetBool("is_active", true)
	o.SetFloat64("account_balance", 259.75)
	o.SetString("signup_date_str", "2023-08-15")
	o.SetString("last_login_date_iso", "2025-09-13T13:20:00Z")
	o.SetInt("birth_year", 1996)
	o.SetString("phone_number", "+14155555671")
	o.SetString("preferred_language", "en")
	o.SetString("time_zone", "Europe/Berlin")
	o.SetInt("loyalty_points", 845)
	o.SetFloat64("avg_session_length_minutes", 14.3)
	o.SetBool("newsletter_subscribed", false)
	o.SetString("ip_address", "192.168.0.42")
	o.SetNull("notes")

	if o.NumFields() != 16 {
		t.Errorf("Object NumFields() = %d; want 16", o.NumFields())
	}

	checkInt := func(key string, expected int) {
		if val := o.Int(key); val != expected {
			t.Errorf("Int(%q) = %d; want %d", key, val, expected)
		}
	}
	checkString := func(key, expected string) {
		if val := o.String(key); val != expected {
			t.Errorf("String(%q) = %q; want %q", key, val, expected)
		}
	}
	checkBool := func(key string, expected bool) {
		if val := o.Bool(key); val != expected {
			t.Errorf("Bool(%q) = %v; want %v", key, val, expected)
		}
	}
	checkFloat64 := func(key string, expected float64) {
		if val := o.Float64(key); val != expected {
			t.Errorf("Float64(%q) = %f; want %f", key, val, expected)
		}
	}

	checkInt("user_id", 12345)
	checkString("username", "jdoe")
	checkString("email_address", "jdoe@example.com")
	checkBool("is_active", true)
	checkFloat64("account_balance", 259.75)
	checkString("signup_date_str", "2023-08-15")
	checkString("last_login_date_iso", "2025-09-13T13:20:00Z")
	checkInt("birth_year", 1996)
	checkString("phone_number", "+14155555671")
	checkString("preferred_language", "en")
	checkString("time_zone", "Europe/Berlin")
	checkInt("loyalty_points", 845)
	checkFloat64("avg_session_length_minutes", 14.3)
	checkBool("newsletter_subscribed", false)
	checkString("ip_address", "192.168.0.42")

	if fmt.Sprintf("%x", sha256.Sum256(b.Bytes())) != "500b128608cde5dd08b0ec22eb922375441a93f6aad902aaf6c394b77088b4db" {
		t.Errorf("Buffer contents do not match reference implementation")
	}
}

func TestOverwrite(t *testing.T) {
	b := New(nil)
	o := b.SetRootObject()

	o.SetInt("foo", 3)
	o.SetInt("foo", 5)

	if foo := o.Int("foo"); foo != 5 {
		t.Errorf("foo = %d; want 5", foo)
	}

	// overwrite a short value with a longer one (forcing an append)
	short := "hi"
	long := "hello my friend, how are you doing today?"
	o.SetBytes("bar", []byte(short))
	o.SetBytes("bar", []byte(long))

	if bar := o.RawBytes("bar"); !bytes.Equal(bar, []byte(long)) {
		t.Errorf("bar = %q; want %q", bar, long)
	}

	// overwrite with shorter value again; old value should have been zeroed
	o.SetBytes("bar", []byte(short))
	if bar := o.RawString("bar"); bar != short {
		t.Errorf("bar = %q; want %q", bar, short)
	}
	i := bytes.Index(b.buf, []byte(short))
	excess := b.buf[i+len(short):][:len(long)-len(short)]
	for i := range excess {
		if excess[i] != 0 {
			t.Errorf("Old value not zeroed")
		}
	}
}

func TestNesting(t *testing.T) {
	b := New(nil)

	o := b.SetRootObject()
	o.SetObject("foo").SetObject("bar").SetInt("baz", 3)

	if baz := o.Object("foo").Object("bar").Int("baz"); baz != 3 {
		t.Errorf("Nested baz = %d; want 3", baz)
	}

	it := o.Iter()
	e := it.Next()
	if err := it.Err(); err != nil {
		t.Fatalf("Iter Next error: %v", err)
	} else if e.Key != "foo" || e.Value.Type != TypeObject {
		t.Errorf("Iter Next = (%q, %v); want (\"foo\", TypeObject)", e.Key, e.Value.Type)
	}

	foo := e.Value.Object()
	it = foo.Iter()
	e = it.Next()
	if err := it.Err(); err != nil {
		t.Fatalf("Iter Next error: %v", err)
	} else if e.Key != "bar" || e.Value.Type != TypeObject {
		t.Errorf("Iter Next = (%q, %v); want (\"bar\", TypeObject)", e.Key, e.Value.Type)
	}
	bar := e.Value.Object()
	if baz := bar.Int("baz"); baz != 3 {
		t.Errorf("Nested baz via Iter = %d; want 3", baz)
	}
}

func TestNestedSplitRightChild(t *testing.T) {
	// This exercises a split in a nested container and then forces a lookup
	// into the right child, which requires childOff[1] to be intact.
	b := New(nil)
	o := b.SetRootObject()
	nested := o.SetObject("obj")

	candidates := []string{
		"k00", "k01", "k02", "k03", "k04",
		"k05", "k06", "k07", "k08", "k09",
		"k10", "k11", "k12", "k13", "k14",
		"k15", "k16", "k17", "k18", "k19",
	}
	type keyHashPair struct {
		key  string
		hash uint32
	}
	seen := make(map[uint32]bool, len(candidates))
	keys := make([]keyHashPair, 0, len(candidates))
	for _, key := range candidates {
		hash := keyHash(key)
		if seen[hash] {
			continue
		}
		seen[hash] = true
		keys = append(keys, keyHashPair{key: key, hash: hash})
	}
	if len(keys) < 8 {
		t.Fatalf("need at least 8 unique hashes, got %d", len(keys))
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].hash < keys[j].hash
	})

	for i := range 7 {
		nested.SetInt(keys[i].key, i)
	}
	// Insert a key whose hash is greater than the median of the first 7.
	nested.SetInt(keys[7].key, 77)

	for i := range 7 {
		if v := nested.Int(keys[i].key); v != i {
			t.Errorf("nested.Int(%q) = %d; want %d", keys[i].key, v, i)
		}
	}
	if v := nested.Int(keys[7].key); v != 77 {
		t.Errorf("nested.Int(%q) = %d; want 77", keys[7].key, v)
	}
	if nested.NumFields() != 8 {
		t.Errorf("nested.NumFields() = %d; want 8", nested.NumFields())
	}
}

func TestSplitMedianMatch(t *testing.T) {
	b := New(nil)
	a := b.SetRootArray()

	// fill root to capacity, then overwrite median
	for i := range 7 {
		a.SetInt(i, i*10)
	}
	a.SetInt(3, 999)

	for i := range 7 {
		v := a.Int(i)
		if i == 3 {
			if v != 999 {
				t.Errorf("Int(3) = %d; want 999", v)
			}
		} else {
			if v != i*10 {
				t.Errorf("Int(%d) = %d; want %d", i, v, i*10)
			}
		}
	}
}

func TestHashCollision(t *testing.T) {
	b := New(nil)
	o := b.SetRootObject()

	if keyHash("ab") != keyHash("bA") {
		t.Fatal("keys do not collide")
	}
	o.SetInt("ab", 1)
	o.SetInt("bA", 2)

	if o.NumFields() != 2 {
		t.Errorf("NumFields() = %d; want 2", o.NumFields())
	}
	if v := o.Int("ab"); v != 1 {
		t.Errorf("Int(\"ab\") = %d; want 1", v)
	}
	if v := o.Int("bA"); v != 2 {
		t.Errorf("Int(\"bA\") = %d; want 2", v)
	}
}

func TestIterMultiLevel(t *testing.T) {
	b := New(nil)
	a := b.SetRootArray()

	const n = 20
	for i := range n {
		a.SetInt(i, i*10)
	}

	it := a.Iter()
	for i := range n {
		e := it.Next()
		if err := it.Err(); err != nil {
			t.Fatalf("Iter error at index %d: %v", i, err)
		}
		if e == nil {
			t.Fatalf("Iter returned nil at index %d; expected element", i)
		}
		if e.Index != i {
			t.Errorf("Iter element index = %d; want %d", e.Index, i)
		}
		if e.Value.Type != TypeInt {
			t.Errorf("Iter element type = %d; want TypeInt (%d)", e.Value.Type, TypeInt)
		}
		if val := e.Value.Int(); val != i*10 {
			t.Errorf("Iter element value = %d; want %d", val, i*10)
		}
	}
	if e := it.Next(); e != nil {
		t.Errorf("Iter returned extra element after %d items", n)
	}
	if err := it.Err(); err != nil {
		t.Fatalf("Iter error at end: %v", err)
	}
}

func BenchmarkJohnDoe(b *testing.B) {
	buf := New(make([]byte, 0, 2048))
	for b.Loop() {
		buf.buf = buf.buf[:0]
		o := buf.SetRootObject()

		o.SetInt("user_id", 12345)
		o.SetString("username", "jdoe")
		o.SetString("email_address", "jdoe@example.com")
		o.SetBool("is_active", true)
		o.SetFloat64("account_balance", 259.75)
		o.SetString("signup_date_str", "2023-08-15")
		o.SetString("last_login_date_iso", "2025-09-13T13:20:00Z")
		o.SetInt("birth_year", 1996)
		o.SetString("phone_number", "+14155555671")
		o.SetString("preferred_language", "en")
		o.SetString("time_zone", "Europe/Berlin")
		o.SetInt("loyalty_points", 845)
		o.SetFloat64("avg_session_length_minutes", 14.3)
		o.SetBool("newsletter_subscribed", false)
		o.SetString("ip_address", "192.168.0.42")
		o.SetNull("notes")

		_ = o.Int("user_id")
		_ = o.RawString("username")
		_ = o.RawString("email_address")
		_ = o.Bool("is_active")
		_ = o.Float64("account_balance")
		_ = o.RawString("signup_date_str")
		_ = o.RawString("last_login_date_iso")
		_ = o.Int("birth_year")
		_ = o.RawString("phone_number")
		_ = o.RawString("preferred_language")
		_ = o.RawString("time_zone")
		_ = o.Int("loyalty_points")
		_ = o.Float64("avg_session_length_minutes")
		_ = o.Bool("newsletter_subscribed")
		_ = o.RawString("ip_address")
	}
}
