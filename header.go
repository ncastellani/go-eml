// Header parsing functionality.

package eml

import (
	"bytes"
)

func split(ts []token, s token) [][]token {
	r, l := [][]token{}, 0
	for i, t := range ts {
		if string(t) == string(s) {
			r = append(r, ts[l:i])
			l = i + 1
		}
	}
	if l != len(ts) {
		r = append(r, ts[l:])
	}
	return r
}

// BUG: We don't currently support domain literals with commas.
func parseAddressList(s []byte) ([]Address, error) {
	al := []Address{}

	// UTF8 decode the address list
	dd, e := decodeRFC2047(s)
	if e == nil {
		s = []byte(dd)
	}

	ts, e := tokenize(s)
	if e != nil {
		return al, e
	}

	// split by groups (,)
	stb := split(ts, []byte{','})
	var lsb []token
	var vsb [][]token

	for i, t := range stb {
		var p []token
		var fc []byte

		for _, c := range t {
			p = append(p, c)
			fc = append(fc, c...)
		}

		lsb = append(lsb, p...)
		if i != len(stb)-1 && !bytes.Contains(fc, []byte("@")) {
			comma := []byte(",")
			lsb = append(lsb, []token{comma}...)
		}

		if bytes.Contains(fc, []byte("@")) || i == len(stb)-1 {
			vsb = append(vsb, lsb)
			lsb = make([]token, 0)
		}
	}

	for _, ts := range vsb {
		a, e := parseAddress(ts)
		if e != nil {
			return al, e
		}
		al = append(al, a)
	}

	return al, nil
}
