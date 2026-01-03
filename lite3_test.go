package lite3

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
)

// panics -> errors or bools
// jq-style Path(...)
// fuzzing/hardening against untrusted buffers

// feedback:
// - ditch generation system
//   - maybe not; could be used to cache nodes in container?
// - ditch C strings
// - splitting should reset left child's gen to 0
//   - maybe set typ too?
// - treeNodes can be unaligned if inserted in-place

// treeNode and treeNodePtr; keyVal and keyValPtr

func TestBasic(t *testing.T) {
	b := New(nil)
	o := b.SetRootObject()

	o.Set("foo", Bool(true))
	if val := o.Get("foo").Bool(); val != true {
		t.Errorf(`Object.Bool("foo") = %v; want true`, val)
	}

	a := b.SetRootArray()
	a.Set(a.Len(), String("hello"))
	a.Set(a.Len(), String("world"))
	if val := a.Get(0).String(); val != "hello" {
		t.Errorf(`Index(0).String() = %q; want "hello"`, val)
	}
	if val := a.Get(1).String(); val != "world" {
		t.Errorf(`Index(1).String() = %q; want "world"`, val)
	}
}

func TestKeyNotFound(t *testing.T) {
	b := New(nil)
	o := b.SetRootObject()
	if v := o.Get("foo"); v.Type != TypeInvalid {
		t.Errorf("Expected invalid Value for missing key; got %v", v)
	}
	a := b.SetRootArray()
	a.Set(0, Bool(true))
	if v := a.Get(1); v.Type != TypeInvalid {
		t.Errorf("Expected invalid Value for missing element; got %v", v)
	}
}

func TestIter(t *testing.T) {
	b := New(nil)

	o := b.SetRootObject()
	o.Set("lap", Int(55))
	o.Set("time_sec", Float64(88.427))
	o.Set("username", String("jdoe"))

	var s strings.Builder
	for key, val := range o.All() {
		fmt.Fprintf(&s, "%s=%v;", key, val)
	}
	if exp := "lap=55;time_sec=88.427;username=jdoe;"; s.String() != exp {
		t.Errorf("Object All() = %q; want %q", s.String(), exp)
	}

	a := b.SetRootArray()
	a.Set(0, Int(10))
	a.Set(1, Int(20))
	a.Set(2, Int(30))

	var x int
	for i, val := range a.All() {
		x += (i + 1) * val.Int()
	}
	if x != 140 {
		t.Errorf("Array All() computed value = %d; want 140", x)
	}
}

func TestJSON(t *testing.T) {
	b := New(nil)
	o := b.SetRootObject()
	o.Set("event", String("lap_complete"))
	o.Set("lap", Int(56))
	o.Set("time_sec", Float64(88.427))

	js, _ := json.Marshal(b)
	if string(js) != `{"lap":56,"event":"lap_complete","time_sec":88.427}` {
		t.Errorf("Unexpected JSON: %s", js)
	}

	b2 := New(nil)
	if err := json.Unmarshal(js, b2); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	o2 := b2.Root().(Object)
	if lap := o2.Get("lap").Int(); lap != 56 {
		t.Errorf("Unmarshaled lap = %d; want 56", lap)
	}
	if event := o2.Get("event").String(); event != "lap_complete" {
		t.Errorf("Unmarshaled event = %q; want \"lap_complete\"", event)
	}
	if timeSec := o2.Get("time_sec").Float64(); timeSec != 88.427 {
		t.Errorf("Unmarshaled time_sec = %f; want 88.427", timeSec)
	}
}

func TestJohnDoe(t *testing.T) {
	b := New(make([]byte, 0, 2048))
	o := b.SetRootObject()

	o.Set("user_id", Int(12345))
	o.Set("username", String("jdoe"))
	o.Set("email_address", String("jdoe@example.com"))
	o.Set("is_active", Bool(true))
	o.Set("account_balance", Float64(259.75))
	o.Set("signup_date_str", String("2023-08-15"))
	o.Set("last_login_date_iso", String("2025-09-13T13:20:00Z"))
	o.Set("birth_year", Int(1996))
	o.Set("phone_number", String("+14155555671"))
	o.Set("preferred_language", String("en"))
	o.Set("time_zone", String("Europe/Berlin"))
	o.Set("loyalty_points", Int(845))
	o.Set("avg_session_length_minutes", Float64(14.3))
	o.Set("newsletter_subscribed", Bool(false))
	o.Set("ip_address", String("192.168.0.42"))
	o.Set("notes", Null())

	if o.NumFields() != 16 {
		t.Errorf("Object NumFields() = %d; want 16", o.NumFields())
	}

	checkInt := func(key string, expected int) {
		if val := o.Get(key).Int(); val != expected {
			t.Errorf("Int(%q) = %d; want %d", key, val, expected)
		}
	}
	checkString := func(key, expected string) {
		if val := o.Get(key).String(); val != expected {
			t.Errorf("String(%q) = %q; want %q", key, val, expected)
		}
	}
	checkBool := func(key string, expected bool) {
		if val := o.Get(key).Bool(); val != expected {
			t.Errorf("Bool(%q) = %v; want %v", key, val, expected)
		}
	}
	checkFloat64 := func(key string, expected float64) {
		if val := o.Get(key).Float64(); val != expected {
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

	o.Set("foo", Int(3))
	o.Set("foo", Int(5))

	if foo := o.Get("foo").Int(); foo != 5 {
		t.Errorf("foo = %d; want 5", foo)
	}

	// overwrite a short value with a longer one (forcing an append)
	short := "hi"
	long := "hello my friend, how are you doing today?"
	o.Set("bar", Bytes([]byte(short)))
	o.Set("bar", Bytes([]byte(long)))

	if bar := o.Get("bar").RawBytes(); !bytes.Equal(bar, []byte(long)) {
		t.Errorf("bar = %q; want %q", bar, long)
	}

	// overwrite with shorter value again; old value should have been zeroed
	o.Set("bar", Bytes([]byte(short)))
	if bar := o.Get("bar").RawString(); bar != short {
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

// Bug: overwriting a value can result in an unaligned node
func TestOverwriteContainerUnaligned(t *testing.T) {
	b := New(nil)
	o := b.SetRootObject()

	o.Set("k", Bytes(make([]byte, nodeSize-1)))
	obj := o.SetObject("k")
	if obj.c.off%4 == 0 {
		t.Fatalf("container offset = %d; want unaligned (mod 4 != 0)", obj.c.off)
	}
}

func TestNesting(t *testing.T) {
	b := New(nil)

	o := b.SetRootObject()
	o.SetObject("foo").SetObject("bar").Set("baz", Int(3))

	if baz := o.Get("foo").Object().Get("bar").Object().Get("baz").Int(); baz != 3 {
		t.Errorf("Nested baz = %d; want 3", baz)
	}

	for key, val := range o.All() {
		if key != "foo" || val.Type != TypeObject {
			t.Errorf("Outer key/value = (%q, %v); want (\"foo\", TypeObject)", key, val.Type)
		}
		foo := val.Object()
		for key2, val2 := range foo.All() {
			if key2 != "bar" || val2.Type != TypeObject {
				t.Errorf("Inner key/value = (%q, %v); want (\"bar\", TypeObject)", key2, val2.Type)
			}
			bar := val2.Object()
			if baz := bar.Get("baz").Int(); baz != 3 {
				t.Errorf("Nested baz via All() = %d; want 3", baz)
			}
		}
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
		nested.Set(keys[i].key, Int(i))
	}
	// Insert a key whose hash is greater than the median of the first 7.
	nested.Set(keys[7].key, Int(77))

	for i := range 7 {
		if v := nested.Get(keys[i].key).Int(); v != i {
			t.Errorf("nested.Int(%q) = %d; want %d", keys[i].key, v, i)
		}
	}
	if v := nested.Get(keys[7].key).Int(); v != 77 {
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
		a.Set(i, Int(i*10))
	}
	a.Set(3, Int(999))

	for i := range 7 {
		v := a.Get(i).Int()
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
	o.Set("ab", Int(1))
	o.Set("bA", Int(2))

	if o.NumFields() != 2 {
		t.Errorf("NumFields() = %d; want 2", o.NumFields())
	}
	if v := o.Get("ab").Int(); v != 1 {
		t.Errorf("Int(\"ab\") = %d; want 1", v)
	}
	if v := o.Get("bA").Int(); v != 2 {
		t.Errorf("Int(\"bA\") = %d; want 2", v)
	}
}

func TestIterMultiLevel(t *testing.T) {
	b := New(nil)
	a := b.SetRootArray()

	const n = 20
	for i := range n {
		a.Set(i, Int(i*10))
	}
	for i, v := range a.All() {
		if v.Int() != i*10 {
			t.Errorf("All() element %d = %d; want %d", i, v.Int(), i*10)
		}
	}
}

func BenchmarkJohnDoe(b *testing.B) {
	b.Run("Lite3", func(b *testing.B) {
		buf := New(make([]byte, 0, 2048))
		for b.Loop() {
			o := buf.SetRootObject()

			o.Set("user_id", Int(12345))
			o.Set("username", String("jdoe"))
			o.Set("email_address", String("jdoe@example.com"))
			o.Set("is_active", Bool(true))
			o.Set("account_balance", Float64(259.75))
			o.Set("signup_date_str", String("2023-08-15"))
			o.Set("last_login_date_iso", String("2025-09-13T13:20:00Z"))
			o.Set("birth_year", Int(1996))
			o.Set("phone_number", String("+14155555671"))
			o.Set("preferred_language", String("en"))
			o.Set("time_zone", String("Europe/Berlin"))
			o.Set("loyalty_points", Int(845))
			o.Set("avg_session_length_minutes", Float64(14.3))
			o.Set("newsletter_subscribed", Bool(false))
			o.Set("ip_address", String("192.168.0.42"))
			o.Set("notes", Null())

			_ = o.Get("user_id").Int()
			_ = o.Get("username").RawString()
			_ = o.Get("email_address").RawString()
			_ = o.Get("is_active").Bool()
			_ = o.Get("account_balance").Float64()
			_ = o.Get("signup_date_str").RawString()
			_ = o.Get("last_login_date_iso").RawString()
			_ = o.Get("birth_year").Int()
			_ = o.Get("phone_number").RawString()
			_ = o.Get("preferred_language").RawString()
			_ = o.Get("time_zone").RawString()
			_ = o.Get("loyalty_points").Int()
			_ = o.Get("avg_session_length_minutes").Float64()
			_ = o.Get("newsletter_subscribed").Bool()
			_ = o.Get("ip_address").RawString()
		}
	})
	b.Run("JSON", func(b *testing.B) {
		var johnDoe = struct {
			UserID                  int     `json:"user_id"`
			Username                string  `json:"username"`
			EmailAddress            string  `json:"email_address"`
			IsActive                bool    `json:"is_active"`
			AccountBalance          float64 `json:"account_balance"`
			SignupDateStr           string  `json:"signup_date_str"`
			LastLoginDateISO        string  `json:"last_login_date_iso"`
			BirthYear               int     `json:"birth_year"`
			PhoneNumber             string  `json:"phone_number"`
			PreferredLanguage       string  `json:"preferred_language"`
			TimeZone                string  `json:"time_zone"`
			LoyaltyPoints           int     `json:"loyalty_points"`
			AvgSessionLengthMinutes float64 `json:"avg_session_length_minutes"`
			NewsletterSubscribed    bool    `json:"newsletter_subscribed"`
			IPAddress               string  `json:"ip_address"`
			Notes                   *string `json:"notes"`
		}{
			UserID:                  12345,
			Username:                "jdoe",
			EmailAddress:            "jdoe@example.com",
			IsActive:                true,
			AccountBalance:          259.75,
			SignupDateStr:           "2023-08-15",
			LastLoginDateISO:        "2025-09-13T13:20:00Z",
			BirthYear:               1996,
			PhoneNumber:             "+14155555671",
			PreferredLanguage:       "en",
			TimeZone:                "Europe/Berlin",
			LoyaltyPoints:           845,
			AvgSessionLengthMinutes: 14.3,
			NewsletterSubscribed:    false,
			IPAddress:               "192.168.0.42",
			Notes:                   nil,
		}
		for b.Loop() {
			js, _ := json.Marshal(johnDoe)
			json.Unmarshal(js, &johnDoe)
		}
	})
}
