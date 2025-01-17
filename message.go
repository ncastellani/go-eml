package eml

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime/quotedprintable"
	"net/textproto"
	"regexp"
	"strings"
	"time"
)

type Message struct {
	Headers []byte // full message headers
	Body    []byte // message body separated from headers

	// from headers
	ParsedHeaders map[string][]string // all headers

	MessageID   string
	Date        time.Time
	Sender      Address
	From        []Address
	ReplyTo     []Address
	To          []Address
	Cc          []Address
	Bcc         []Address
	Subject     string
	ContentType string
	Comments    []string
	Keywords    []string
	InReply     []string
	References  []string

	// from body
	Text        string
	Html        string
	Attachments []Attachment
	Parts       []Part
}

type Attachment struct {
	Filename string
	Data     []byte
}

func Parse(data []byte) (msg Message, errors []error) {

	// treat the raw data
	raw, err := ParseRaw(data)
	if err != nil {
		errors = append(errors, fmt.Errorf("raw parsing: %v", err))
		return
	}

	// proccess the message headers and body parts
	msg, errors = handleMessage(raw)

	// append the body and headers at the message
	msg.Body = raw.Body
	msg.Headers = extractHeaders(&raw.Body, &data)

	return
}

// extract the data from each header and parse the body contents
func handleMessage(r RawMessage) (msg Message, errors []error) {

	// proccess and append the headers parameters
	msg.ParsedHeaders = make(map[string][]string)
	for _, rh := range r.RawHeaders {

		// add this header to the parsed headers map
		if _, ok := msg.ParsedHeaders[string(rh.Key)]; !ok {
			msg.ParsedHeaders[string(rh.Key)] = []string{}
		}

		msg.ParsedHeaders[string(rh.Key)] = append(msg.ParsedHeaders[string(rh.Key)], string(rh.Value))

		// handle key headers
		var err error

		switch strings.ToLower(string(rh.Key)) {
		case `content-type`:
			msg.ContentType = string(rh.Value)
		case `message-id`:
			v := bytes.TrimSpace(rh.Value)
			v = bytes.Trim(rh.Value, `<>`)
			msg.MessageID = string(v)
		case `in-reply-to`:
			ids := strings.Fields(string(rh.Value))
			for _, id := range ids {
				msg.InReply = append(msg.InReply, strings.Trim(id, `<> `))
			}
		case `references`:
			ids := strings.Fields(string(rh.Value))
			for _, id := range ids {
				msg.References = append(msg.References, strings.Trim(id, `<> `))
			}
		case `date`:
			msg.Date = ParseDate(string(rh.Value))
		case `from`:
			msg.From, err = parseAddressList(rh.Value)
		case `sender`:
			msg.Sender, err = ParseAddress(rh.Value)
		case `reply-to`:
			msg.ReplyTo, err = parseAddressList(rh.Value)
		case `to`:
			msg.To, err = parseAddressList(rh.Value)
		case `cc`:
			msg.Cc, err = parseAddressList(rh.Value)
		case `bcc`:
			msg.Bcc, err = parseAddressList(rh.Value)
		case `subject`:
			subject, e := Decode(rh.Value)
			err = e
			msg.Subject = string(subject)
		case `comments`:
			msg.Comments = append(msg.Comments, string(rh.Value))
		case `keywords`:
			ks := strings.Split(string(rh.Value), ",")
			for _, k := range ks {
				msg.Keywords = append(msg.Keywords, strings.TrimSpace(k))
			}
		}

		if err != nil {
			errors = append(errors, fmt.Errorf("header parser: %v", err))
			err = nil
		}
	}

	// if no sender header was found, use the first value of From
	if msg.Sender == nil && len(msg.From) > 0 {
		msg.Sender = msg.From[0]
	}

	// do the body parsing
	if msg.ContentType != `` {

		// try to parse the body contents with the passed content type
		parts, e := parseBody(msg.ContentType, r.Body, textproto.MIMEHeader{})
		if e != nil {
			msg.Text = string(r.Body) // set the whole message body as the message text
			errors = append(errors, fmt.Errorf("body parser: %v", e))
			return
		}

		// handle each message part
		for k, part := range parts {
			switch {
			case strings.Contains(part.Type, "text/plain"):
				part.Data, e = decodeContentTransferEncoding(msg.ParsedHeaders, part.Headers, &part.Data)
				if e != nil {
					errors = append(errors, e)
				}

				data, e := UTF8(part.Charset, part.Data)
				if e != nil {
					msg.Text = string(part.Data)
				} else {
					msg.Text = string(data)
					parts[k].Data = data
				}

				//
			case strings.Contains(part.Type, "text/html"):
				part.Data, e = decodeContentTransferEncoding(msg.ParsedHeaders, part.Headers, &part.Data)
				if e != nil {
					errors = append(errors, e)
				}

				data, e := UTF8(part.Charset, part.Data)
				if e != nil {
					msg.Html = string(part.Data)
				} else {
					msg.Html = string(data)
					parts[k].Data = data
				}

				//
			default:
				if cd, ok := part.Headers["Content-Disposition"]; ok {
					if strings.Contains(cd[0], "attachment") {
						filename := regexp.MustCompile("(?msi)name=\"(.*?)\"").FindStringSubmatch(cd[0]) //.FindString(cd[0])
						if len(filename) < 2 {
							errors = append(errors, fmt.Errorf("body parser: failed get filename from header Content-Disposition"))
							break
						}

						dfilename, e := Decode([]byte(filename[1]))
						if e != nil {
							errors = append(errors, fmt.Errorf("body parser: failed decode filename of attachment [msg: %v]", e))
						} else {
							filename[1] = string(dfilename)
						}

						part.Data, e = decodeContentTransferEncoding(msg.ParsedHeaders, part.Headers, &part.Data)
						if e != nil {
							errors = append(errors, e)
						}

						msg.Attachments = append(msg.Attachments, Attachment{filename[1], part.Data})
					}
				}
			}
		}

		msg.Parts = parts
		msg.ContentType = parts[0].Type
		msg.Text = string(parts[0].Data)
	} else {
		msg.Text = string(r.Body)
	}

	return
}

// get the headers from the full message and sanitize its suffix
func extractHeaders(body *[]byte, data *[]byte) []byte {

	// replace the body from the full message to get just the headers
	headers := bytes.Replace(*data, *body, nil, 1)

	// define a list of CF + LF variations at the headers end
	trimOut := [][]byte{
		[]byte("\n\r\n"),
		[]byte("\r\n\n"),
		[]byte("\r\n"),
		[]byte("\n\r"),
		[]byte("\n"),
		[]byte("\r"),
	}

	// trum each item of the list above from the headers suffix
	for _, i := range trimOut {
		headers = bytes.TrimSuffix(headers, i)
	}

	return headers
}

// generic function to handle content encoding
func decodeContentTransferEncoding(msgHeaders, partHeaders map[string][]string, toDecode *[]byte) (decoded []byte, err error) {
	decoded = *toDecode

	// read the encoding from the part headers
	// if it does not exists in that map, use the message headers
	encoding := ""

	if headerEncoding, ok := partHeaders["Content-Transfer-Encoding"]; ok {
		encoding = strings.ToLower(headerEncoding[0])
	} else {
		if headerEncoding, ok := msgHeaders["Content-Transfer-Encoding"]; ok {
			encoding = strings.ToLower(headerEncoding[0])
		}
	}

	// parse the transfer encoding
	switch strings.ToLower(encoding) {
	case "base64":
		decoded, err = base64.StdEncoding.DecodeString(string(*toDecode))
		if err != nil {
			return decoded, fmt.Errorf("body parser: failed decode base64 [msg: %v]", err)
		}
	case "quoted-printable":
		decoded, _ = io.ReadAll(quotedprintable.NewReader(bytes.NewReader(*toDecode)))
	}

	return
}
