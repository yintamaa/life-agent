package mail

import (
	"strings"
	"testing"
)

const sample = "From: =?UTF-8?B?5byg5LiJ?= <zs@example.com>\r\n" +
	"Subject: =?UTF-8?B?5ZGo5oql?=\r\n" +
	"Message-ID: <b@x>\r\n" +
	"References: <a@x> <b0@x>\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/alternative; boundary=BB\r\n\r\n" +
	"--BB\r\nContent-Type: text/html; charset=gbk\r\n\r\n<p>\xc4\xe3\xba\xc3</p>\r\n" +
	"--BB--\r\n"

func TestParseBody(t *testing.T) {
	refs, body := parseBody(strings.NewReader(sample))
	if len(refs) != 2 || refs[0] != "a@x" {
		t.Fatalf("refs %v", refs)
	}
	if body != "你好" { // GBK html -> utf-8 plain
		t.Fatalf("body %q", body)
	}
	if got := subjPrefix.ReplaceAllString("Re: 回复：周报", ""); got != "周报" {
		t.Fatalf("subject %q", got)
	}
}
