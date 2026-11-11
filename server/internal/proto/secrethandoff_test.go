package proto

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Only a plain no passes for a decline of a hand-off card: anything else set beside it makes the answer an approval,
// which only a device that may approve gives.
func TestSecretHandoffDeclines(t *testing.T) {
	for body, want := range map[string]bool{
		`{"allow":false}`:                       true,
		`{"dismiss":true}`:                      true,
		`{"cancel":true}`:                       true,
		`{"allow":false,"message":"not now"}`:   true,
		`{"allow":false,"feedback":"no"}`:       true,
		`{}`:                                    false,
		`{"allow":true}`:                        false,
		`{"allow":false,"always":true}`:         false,
		`{"dismiss":true,"allow":true}`:         false,
		`{"allow":false,"approve":true}`:        false,
		`{"dismiss":true,"option":"1"}`:         false,
		`{"dismiss":true,"options":["1"]}`:      false,
		`{"allow":false,"keys":["1"]}`:          false,
		`{"allow":false,"pre_keys":["1"]}`:      false,
		`{"allow":false,"text":"y"}`:            false,
		`{"cancel":true,"answers":{"q":"yes"}}`: false,
		`{"allow":false,"values":{"k":"v"}}`:    false,
		`{"allow":false,"content":{"k":"v"}}`:   false,
		`{"dismiss":true,"action":"accept"}`:    false,
		`{"allow":false,"mode":"default"}`:      false,
		`{"allow":false,"remembered":true}`:     false,
		`{"approve":false}`:                     false, // a plan card's no: not a hand-off card's answer
		`{"allow":false,"approve":false}`:       false,
		`{"allow":false,"answers":{}}`:          true, // empty: nothing more than the no
		`{"dismiss":true,"reason":"x"}`:         true,
	} {
		var r Resolution
		if err := json.Unmarshal([]byte(body), &r); err != nil {
			t.Fatal(err)
		}
		if got := SecretHandoffDeclines(r); got != want {
			t.Errorf("%s: declines %v, want %v", body, got, want)
		}
	}
}

// Every Resolution field is omitempty: SecretHandoffDeclines relies on it to see a field it does not know as more than
// a no. A field without it would make every decline an approval (refused from the devices that may not approve).
func TestResolutionFieldsAreOmitempty(t *testing.T) {
	rt := reflect.TypeOf(Resolution{})
	for i := range rt.NumField() {
		if f := rt.Field(i); !strings.Contains(f.Tag.Get("json"), ",omitempty") {
			t.Errorf("Resolution.%s is not omitempty", f.Name)
		}
	}
}
