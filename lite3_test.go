package lite3

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"testing"
)

func TestBasic(t *testing.T) {
	b := New(make([]byte, 0, 1024))
	o := b.SetRootObject()
	o.SetBool("foo", true)

	if val := o.Bool("foo"); val != true {
		t.Errorf(`Object.Bool("foo") = %v; want true`, val)
	}

	a := b.SetRootArray()
	a.SetString(a.Count(), "hello")
	a.SetString(a.Count(), "world")
	if val := a.String(0); val != "hello" {
		t.Errorf(`Index(0).String() = %q; want "hello"`, val)
	}
	if val := a.String(1); val != "world" {
		t.Errorf(`Index(1).String() = %q; want "world"`, val)
	}
}

func TestIter(t *testing.T) {
	b := New(make([]byte, 0, 1024))

	o := b.SetRootObject()
	o.SetInt("lap", 55)
	o.SetFloat64("time_sec", 88.427)
	o.SetString("username", "jdoe")

	it := o.Iter()
	key, typ, val, err := it.Next()
	if err != nil {
		t.Fatalf("Iter Next error: %v", err)
	} else if key != "lap" || typ != TypeInt {
		t.Errorf("Iter Next = (%q, %v); want (\"lap\", TypeInt)", key, typ)
	} else if val := binary.LittleEndian.Uint64(val); val != 55 {
		t.Errorf("Iter Next lap value = %d; want 55", val)
	}
	key, typ, val, err = it.Next()
	if err != nil {
		t.Fatalf("Iter Next error: %v", err)
	} else if key != "time_sec" || typ != TypeFloat {
		t.Errorf("Iter Next = (%q, %v); want (\"time_sec\", TypeFloat)", key, typ)
	} else if val := binary.LittleEndian.Uint64(val); val != math.Float64bits(88.427) {
		t.Errorf("Iter Next time_sec value = %d; want %d", val, math.Float64bits(88.427))
	}
	key, typ, val, err = it.Next()
	if err != nil {
		t.Fatalf("Iter Next error: %v", err)
	} else if key != "username" || typ != TypeString {
		t.Errorf("Iter Next = (%q, %v); want (\"username\", TypeString)", key, typ)
	} else if slen := binary.LittleEndian.Uint32(val); slen != 5 {
		t.Errorf("Iter Next username length = %d; want 5", slen)
	} else if string(val[4:][:slen-1]) != "jdoe" {
		t.Errorf("Iter Next username value = %q; want \"jdoe\"", val[4:][:slen])
	}
	_, _, _, err = it.Next()
	if err != io.EOF {
		t.Fatalf("Expected Iter Next to return EOF at end of buffer")
	}
}

func TestJSON(t *testing.T) {
	t.SkipNow()
	b := New(make([]byte, 0, 1024))

	o := b.SetRootObject()
	o.SetString("event", "lap_complete")
	o.SetInt("lap", 55)
	o.SetFloat64("time_sec", 88.427)

	js, _ := json.Marshal(o)
	if string(js) != `{"event":"lap_complete","lap":55,"time_sec":88.427}` {
		t.Errorf("Unexpected JSON: %s", js)
	}

	o.SetInt("lap", 56)
	js, _ = json.Marshal(o)
	if string(js) != `{"event":"lap_complete","lap":56,"time_sec":88.427}` {
		t.Errorf("Unexpected JSON: %s", js)
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

	if o.Count() != 16 {
		t.Errorf("Object Count() = %d; want 16", o.Count())
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
	b := New(make([]byte, 0, 1024))
	o := b.SetRootObject()

	o.SetObject("nested").SetInt("foo", 3)

	if foo := o.Object("nested").Int("foo"); foo != 3 {
		t.Errorf("Nested foo = %d; want 3", foo)
	}
}

func TestNesting(t *testing.T) {
	b := New(make([]byte, 0, 1024))

	o := b.SetRootObject()
	o.SetObject("nested").SetInt("foo", 3)

	if foo := o.Object("nested").Int("foo"); foo != 3 {
		t.Errorf("Nested foo = %d; want 3", foo)
	}
}
