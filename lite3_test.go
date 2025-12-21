package lite3

import (
	"encoding/binary"
	"io"
	"math"
	"testing"
)

func TestIter(t *testing.T) {
	buf := New()

	buf.SetInt("lap", 55)
	buf.SetFloat64("time_sec", 88.427)
	buf.SetString("username", "jdoe")

	it, err := buf.Iter(0)
	if err != nil {
		t.Fatalf("Iter error: %v", err)
	}
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
	buf := New()

	buf.SetString("event", "lap_complete")
	buf.SetInt("lap", 55)
	buf.SetFloat64("time_sec", 88.427)

	js, _ := buf.MarshalJSON()
	if string(js) != `{"event":"lap_complete","lap":55,"time_sec":88.427}` {
		t.Errorf("Unexpected JSON: %s", js)
	}

	buf.SetInt("lap", 56)
	js, _ = buf.MarshalJSON()
	if string(js) != `{"event":"lap_complete","lap":56,"time_sec":88.427}` {
		t.Errorf("Unexpected JSON: %s", js)
	}
}

func TestJohnDoe(t *testing.T) {
	buf := New()

	buf.SetInt("user_id", 12345)
	buf.SetString("username", "jdoe")
	buf.SetString("email_address", "jdoe@example.com")
	buf.SetBool("is_active", true)
	buf.SetFloat64("account_balance", 259.75)
	buf.SetString("signup_date_str", "2023-08-15")
	buf.SetString("last_login_date_iso", "2025-09-13T13:20:00Z")
	buf.SetInt("birth_year", 1996)
	buf.SetString("phone_number", "+14155555671")
	buf.SetString("preferred_language", "en")
	buf.SetString("time_zone", "Europe/Berlin")
	buf.SetInt("loyalty_points", 845)
	buf.SetFloat64("avg_session_length_minutes", 14.3)
	buf.SetBool("newsletter_subscribed", false)
	buf.SetString("ip_address", "192.168.0.42")
	buf.SetNull("notes")

	checkInt := func(key string, expected int) {
		val, err := buf.GetInt(key)
		if err != nil {
			t.Fatalf("GetInt(%q) error: %v", key, err)
		} else if val != expected {
			t.Errorf("GetInt(%q) = %d; want %d", key, val, expected)
		}
	}
	checkString := func(key, expected string) {
		val, err := buf.GetString(key)
		if err != nil {
			t.Fatalf("GetString(%q) error: %v", key, err)
		} else if val != expected {
			t.Errorf("GetString(%q) = %q; want %q", key, val, expected)
		}
	}
	checkBool := func(key string, expected bool) {
		val, err := buf.GetBool(key)
		if err != nil {
			t.Fatalf("GetBool(%q) error: %v", key, err)
		} else if val != expected {
			t.Errorf("GetBool(%q) = %v; want %v", key, val, expected)
		}
	}
	checkFloat64 := func(key string, expected float64) {
		val, err := buf.GetFloat64(key)
		if err != nil {
			t.Fatalf("GetFloat64(%q) error: %v", key, err)
		} else if val != expected {
			t.Errorf("GetFloat64(%q) = %f; want %f", key, val, expected)
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
