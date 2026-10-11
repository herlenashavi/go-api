package logging

// Unit tests for the two validate hooks that keep pattern-based redaction
// from mangling operational data, exercised directly rather than through a
// logger. The end-to-end behavior they produce is covered in pii_test.go;
// these pin the decision boundaries themselves, including the cases that
// would be indistinguishable from the outside (a value excluded by the
// Luhn checksum vs. one excluded by issuer-prefix filtering).

import "testing"

func TestRoutableIPv4(t *testing.T) {
	cases := []struct {
		addr string
		want bool
		why  string
	}{
		{"203.0.113.42", true, "globally routable"},
		{"8.8.8.8", true, "globally routable"},
		{"127.0.0.1", false, "loopback"},
		{"127.0.0.53", false, "loopback"},
		{"0.0.0.0", false, "unspecified — the default bind address"},
		{"10.0.0.5", false, "RFC 1918 10/8"},
		{"10.244.1.7", false, "RFC 1918 10/8 — typical pod IP"},
		{"172.16.0.9", false, "RFC 1918 172.16/12"},
		{"172.31.255.254", false, "RFC 1918 172.16/12 upper bound"},
		{"172.32.0.1", true, "just outside RFC 1918 172.16/12"},
		{"192.168.1.1", false, "RFC 1918 192.168/16"},
		{"169.254.0.1", false, "link-local"},
		{"100.64.0.1", false, "RFC 6598 carrier-grade NAT"},
		{"100.127.255.255", false, "RFC 6598 upper bound"},
		{"100.128.0.1", true, "just outside RFC 6598"},
		{"224.0.0.1", false, "multicast"},
		{"255.255.255.255", false, "broadcast"},
		{"not-an-ip", false, "unparseable"},
	}
	for _, tc := range cases {
		t.Run(tc.addr, func(t *testing.T) {
			if got := routableIPv4(tc.addr); got != tc.want {
				t.Fatalf("routableIPv4(%q) = %v, want %v (%s)", tc.addr, got, tc.want, tc.why)
			}
		})
	}
}

func TestCardNumberLike(t *testing.T) {
	cases := []struct {
		name   string
		number string
		want   bool
	}{
		// Real issuer test numbers: valid prefix, valid length, valid Luhn.
		{"visa 16", "4111111111111111", true},
		{"visa 13", "4222222222222", true},
		{"visa separated", "4111 1111 1111 1111", true},
		{"visa dashed", "4111-1111-1111-1111", true},
		{"mastercard", "5555555555554444", true},
		{"mastercard 2-series", "2223003122003222", true},
		{"amex", "378282246310005", true},
		{"discover", "6011111111111117", true},
		{"diners", "30569309025904", true},
		{"jcb", "3530111333300000", true},
		{"unionpay", "6200000000000005", true},

		// Fails Luhn — excluded by the checksum, as before.
		{"luhn failure", "1234567890123456", false},
		{"visa prefix bad checksum", "4111111111111112", false},

		// Passes Luhn but is not an issuable number: this is what the IIN
		// table adds over the checksum alone.
		{"no issuer prefix (7)", "7992739800002", false},
		{"no issuer prefix (9)", "9000000000000001", false},
		{"epoch micros", "1760000000000008", false},
		{"visa prefix, 17 digits", "40000000000000006", false},
		{"mastercard prefix, 15 digits", "510000000000003", false},
		{"amex prefix, 16 digits", "3400000000000000", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cardNumberLike(tc.number); got != tc.want {
				t.Fatalf("cardNumberLike(%q) = %v, want %v", tc.number, got, tc.want)
			}
		})
	}
}

// TestLuhnValidFixturesPassChecksum guards the premise of
// pii_test.go's TestPII_LuhnValidNonCardNumbersAreNotFlagged: those
// fixtures only demonstrate issuer-prefix filtering if they actually pass
// Luhn. Should one ever be edited into a number that fails the checksum,
// that test would still pass while proving nothing — this catches that.
func TestLuhnValidFixturesPassChecksum(t *testing.T) {
	for _, n := range []string{
		"7992739800002",
		"9000000000000001",
		"1760000000000008",
		"40000000000000006",
	} {
		if !luhnValid(n) {
			t.Errorf("fixture %q does not pass Luhn; it would be excluded by the "+
				"checksum alone and proves nothing about issuer-prefix filtering", n)
		}
	}
}

func TestIsOperationalField_NormalizesSeparators(t *testing.T) {
	for _, key := range []string{"addr", "Addr", "listenAddr", "listen_addr", "LISTEN-ADDR", "grpcAddr"} {
		if !isOperationalField(key) {
			t.Errorf("isOperationalField(%q) = false, want true", key)
		}
	}
	// Keys naming someone else's address are deliberately absent — see
	// operationalFieldNames' doc comment.
	for _, key := range []string{"peer", "peerAddr", "remoteAddr", "clientIp", "note", "detail"} {
		if isOperationalField(key) {
			t.Errorf("isOperationalField(%q) = true, want false", key)
		}
	}
}
