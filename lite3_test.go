package lite3

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"testing"
)

func TestBasic(t *testing.T) {
	c := New(make([]byte, 0, 1024))
	c.SetInt(123)
	if val := c.Int(); val != 123 {
		t.Errorf("Int() = %d; want 123", val)
	}

	c.Reset()
	c.NewObject()
	c.Field("foo")
	c.SetBool(true)

	c.Reset()
	c.Field("foo")
	if val := c.Bool(); val != true {
		t.Errorf(`Field("foo").Bool() = %v; want true`, val)
	}

	c.Reset()
	c.NewArray()
	c.SetString("hello")
	c.Append()
	c.SetString("world")

	c.Reset()
	c.Index(0)
	if val := c.String(); val != "hello" {
		t.Errorf(`Index(0).String() = %q; want "hello"`, val)
	}
	c.Index(1)
	if val := c.String(); val != "world" {
		t.Errorf(`Index(1).String() = %q; want "world"`, val)
	}
}

func TestIter(t *testing.T) {
	c := New(make([]byte, 0, 1024))

	c.NewObject()
	c.Field("lap")
	c.SetInt(55)
	c.Field("time_sec")
	c.SetFloat64(88.427)
	c.Field("username")
	c.SetString("jdoe")

	it := c.Iter()
	key, typ, val, err := it.Next()
	if err != nil {
		t.Fatalf("Iter Next error: %v", err)
	} else if key != "lap" || typ != LITE3_TYPE_I64 {
		t.Errorf("Iter Next = (%q, %v); want (\"lap\", LITE3_TYPE_I64)", key, typ)
	} else if val := binary.LittleEndian.Uint64(val); val != 55 {
		t.Errorf("Iter Next lap value = %d; want 55", val)
	}
	key, typ, val, err = it.Next()
	if err != nil {
		t.Fatalf("Iter Next error: %v", err)
	} else if key != "time_sec" || typ != LITE3_TYPE_F64 {
		t.Errorf("Iter Next = (%q, %v); want (\"time_sec\", LITE3_TYPE_F64)", key, typ)
	} else if val := binary.LittleEndian.Uint64(val); val != math.Float64bits(88.427) {
		t.Errorf("Iter Next time_sec value = %d; want %d", val, math.Float64bits(88.427))
	}
	key, typ, val, err = it.Next()
	if err != nil {
		t.Fatalf("Iter Next error: %v", err)
	} else if key != "username" || typ != LITE3_TYPE_STRING {
		t.Errorf("Iter Next = (%q, %v); want (\"username\", LITE3_TYPE_STRING)", key, typ)
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
	c := New(make([]byte, 0, 1024))

	c.NewObject()
	c.Field("event")
	c.SetString("lap_complete")
	c.Field("lap")
	c.SetInt(55)
	c.Field("time_sec")
	c.SetFloat64(88.427)

	js, _ := json.Marshal(c)
	if string(js) != `{"event":"lap_complete","lap":55,"time_sec":88.427}` {
		t.Errorf("Unexpected JSON: %s", js)
	}

	c.Field("lap")
	c.SetInt(56)
	js, _ = json.Marshal(c)
	if string(js) != `{"event":"lap_complete","lap":56,"time_sec":88.427}` {
		t.Errorf("Unexpected JSON: %s", js)
	}
}

func TestJohnDoe(t *testing.T) {
	c := New(make([]byte, 0, 2048))
	c.NewObject()

	c.Field("user_id")
	c.SetInt(12345)
	c.Field("username")
	c.SetString("jdoe")
	c.Field("email_address")
	c.SetString("jdoe@example.com")
	c.Field("is_active")
	c.SetBool(true)
	c.Field("account_balance")
	c.SetFloat64(259.75)
	c.Field("signup_date_str")
	c.SetString("2023-08-15")
	c.Field("last_login_date_iso")
	c.SetString("2025-09-13T13:20:00Z")
	c.Field("birth_year")
	c.SetInt(1996)
	c.Field("phone_number")
	c.SetString("+14155555671")
	c.Field("preferred_language")
	c.SetString("en")
	c.Field("time_zone")
	c.SetString("Europe/Berlin")
	c.Field("loyalty_points")
	c.SetInt(845)
	c.Field("avg_session_length_minutes")
	c.SetFloat64(14.3)
	c.Field("newsletter_subscribed")
	c.SetBool(false)
	c.Field("ip_address")
	c.SetString("192.168.0.42")
	c.Field("notes")
	c.SetNull()

	if c.Count() != 16 {
		t.Errorf("Object Count() = %d; want 16", c.Count())
	}

	checkInt := func(key string, expected int) {
		c.Field(key)
		if val := c.Int(); val != expected {
			t.Errorf("Field(%q).Int() = %d; want %d", key, val, expected)
		}
	}
	checkString := func(key, expected string) {
		c.Field(key)
		if val := c.String(); val != expected {
			t.Errorf("Field(%q).String() = %q; want %q", key, val, expected)
		}
	}
	checkBool := func(key string, expected bool) {
		c.Field(key)
		if val := c.Bool(); val != expected {
			t.Errorf("Field(%q).Bool() = %v; want %v", key, val, expected)
		}
	}
	checkFloat64 := func(key string, expected float64) {
		c.Field(key)
		if val := c.Float64(); val != expected {
			t.Errorf("Field(%q).Float64() = %f; want %f", key, val, expected)
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
}

func TestNesting(t *testing.T) {
	c := New(make([]byte, 0, 1024))

	c.NewObject()
	c.Field("nested")
	c.NewObject()
	c.Field("foo")
	c.SetInt(3)

	c.Reset()
	c.Field("nested")
	c.Field("foo")
	if foo := c.Int(); foo != 3 {
		t.Errorf("Nested foo = %d; want 3", foo)
	}
}
