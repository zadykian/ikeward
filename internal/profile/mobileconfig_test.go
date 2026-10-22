package profile

import (
	"reflect"
	"strings"
	"testing"
)

func TestDecodePlist(t *testing.T) {
	got, err := decodePlist([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<!-- a comment -->
<dict>
  <key>string</key><string>a &amp; b</string>
  <key>empty</key><string/>
  <key>integer</key><integer>19</integer>
  <key>real</key><real>1.5</real>
  <key>date</key><date>2025-01-29T00:00:00Z</date>
  <key>true</key><true/>
  <key>false</key><false/>
  <key>data</key><data>
    YWJj
    ZGVm
  </data>
  <key>array</key><array><string>a</string><array/><dict/></array>
</dict>
</plist>`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"string": "a & b", "empty": "", "integer": "19", "real": "1.5",
		"date": "2025-01-29T00:00:00Z", "true": true, "false": false, "data": []byte("abcdef"),
		"array": []any{"a", []any(nil), map[string]any{}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decodePlist: %#v, want %#v", got, want)
	}
}

func TestDecodePlistErrors(t *testing.T) {
	for _, test := range []struct{ plist, want string }{
		{"", "EOF"},
		{"<dict/>", "no <plist> element"},
		{"<plist></plist>", "an empty <plist>"},
		{"<plist><dict>", "XML syntax error on line 1: unexpected EOF"},
		{"<plist><dict></plist>", "XML syntax error on line 1: element <dict> closed by </plist>"},
		{"<plist><dict><string>a</string></dict></plist>", "<string> where a dict has a <key>"},
		{"<plist><dict><key>a</key></dict></plist>", `the key "a" has no value`},
		{"<plist><dict><key>a</key><set/></dict></plist>", "<set>, which a plist does not have"},
		{"<plist><array><set/></array></plist>", "<set>, which a plist does not have"},
		{"<plist><data>YWJj!</data></plist>", "illegal base64 data at input byte 4"},
	} {
		_, err := decodePlist([]byte(test.plist))
		if err == nil || err.Error() != test.want {
			t.Errorf("decodePlist(%q): %v, want %s", test.plist, err, test.want)
		}
	}
}

func TestRootPayloadsErrors(t *testing.T) {
	for _, test := range []struct{ mobileconfig, want string }{
		{"\x30\x82\x01\x00", "it is signed, and ikeward reads only an unsigned XML plist"},
		{"bplist00", "not an XML plist: EOF"},
		{"<plist><array/></plist>", "it holds no root payload, com.apple.security.root"},
		{string(newMobileconfig()), "it holds no root payload, com.apple.security.root"},
		{strings.Replace(string(newMobileconfig([]byte("x"))), "<data>", "<string>", 1),
			"not an XML plist: XML syntax error"},
		{rootPayload("<string>MIIB</string>"), "payload 1, a root, holds no data"},
		{rootPayload("<data>YWJj</data>"), "payload 1, a root: x509: malformed certificate"},
	} {
		_, err := rootPayloads(member{"client.mobileconfig", []byte(test.mobileconfig)})
		want := "client.mobileconfig: " + test.want
		if err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("rootPayloads(%q): %v, want %s", test.mobileconfig, err, want)
		}
	}
}

// rootPayload is a plist with one root payload, whose PayloadContent is content.
func rootPayload(content string) string {
	return `<plist><dict><key>PayloadContent</key><array><dict>
<key>PayloadType</key><string>com.apple.security.root</string>
<key>PayloadContent</key>` + content + `
</dict></array></dict></plist>`
}
