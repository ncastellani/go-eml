// Handle multipart messages.

package eml

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"net/textproto"
	"regexp"
	"strings"
)

type Part struct {
	Type    string
	Charset string
	Data    []byte
	Headers map[string][]string
}

// Parse the body of a message, using the given content-type. If the content
// type is multipart, the parts slice will contain an entry for each part
// present; otherwise, it will contain a single entry, with the entire (raw)
// message contents.
func parseBody(ct string, body []byte, ph textproto.MIMEHeader) (parts []Part, err error) {
	mt, ps, err := mime.ParseMediaType(ct)
	if err != nil {
		return
	}

	boundary, ok := ps["boundary"]
	if !ok {
		if strings.HasPrefix(mt, "multipart") {
			return nil, errors.New("multipart specified without boundary")
		}

		// must add the CRLF at the body before calling the mail.readmessage
		// otherwise the passed body will be interpreted as a header
		r := strings.NewReader("\r\n" + string(body))

		m, err := mail.ReadMessage(r)
		if err != nil {
			return nil, err
		}

		// generate the list of headers by joining the found headers
		// with the priority to the textproto ones
		headers := make(map[string][]string)

		for k, v := range m.Header {
			headers[k] = v
		}

		for k, v := range ph {
			headers[k] = v
		}

		parts = append(parts, Part{
			Type:    mt,
			Charset: ps["charset"],
			Data:    body,
			Headers: headers,
		})

		return parts, err
	}

	r := multipart.NewReader(bytes.NewReader(body), boundary)
	p, err := r.NextPart()
	for err == nil {
		// check if this multipart part is empty
		if len(p.Header.Values("Content-Type")) == 0 {
			p, err = r.NextPart()
			continue
		}

		data, _ := io.ReadAll(p) // ignore error
		var subparts []Part
		subparts, err = parseBody(p.Header["Content-Type"][0], data, p.Header)

		if err == nil {
			parts = append(parts, subparts...)
		} else {
			contenttype := regexp.MustCompile("(?is)charset=(.*)").FindStringSubmatch(p.Header["Content-Type"][0])
			charset := "UTF-8"
			if len(contenttype) > 1 {
				charset = contenttype[1]
			}
			part := Part{p.Header["Content-Type"][0], charset, data, p.Header}
			parts = append(parts, part)
		}

		p, err = r.NextPart()
	}

	if err == io.EOF {
		err = nil
	} else if err.Error() == "multipart: NextPart: EOF" {
		err = nil
	}

	return
}
