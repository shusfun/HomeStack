package protocol

import "testing"

func TestJoinDescriptorRoundTrip(t *testing.T) {
	descriptor, err := NewJoinDescriptor("https://app.example.com:8443", "abcdefghijklmnopqrstuvwxyz0123456789_ABCD")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseJoinDescriptor(descriptor.String())
	if err != nil {
		t.Fatal(err)
	}
	if parsed != descriptor {
		t.Fatalf("解析结果不一致: %#v != %#v", parsed, descriptor)
	}
}

func TestJoinDescriptorRejectsHTTP(t *testing.T) {
	_, err := ParseJoinDescriptor("homestack://join?server=http%3A%2F%2Fexample.com&code=abcdefghijklmnopqrstuvwxyz0123456789_ABCD")
	if err == nil {
		t.Fatal("HTTP 控制服务器不应被接受")
	}
}
