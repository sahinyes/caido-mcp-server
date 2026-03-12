package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

var secHeaders = map[string]bool{
	"content-type":                     true,
	"set-cookie":                       true,
	"location":                         true,
	"www-authenticate":                 true,
	"x-frame-options":                  true,
	"x-content-type-options":           true,
	"content-security-policy":          true,
	"strict-transport-security":        true,
	"access-control-allow-origin":      true,
	"access-control-allow-credentials": true,
	"access-control-allow-methods":     true,
	"server":                           true,
	"x-powered-by":                     true,
	"x-ratelimit-limit":               true,
	"x-ratelimit-remaining":           true,
	"retry-after":                      true,
	"cache-control":                    true,
	"transfer-encoding":               true,
	"content-disposition":              true,
	"x-request-id":                    true,
}

type parsedHTTP struct {
	firstLine string
	headers   [][2]string // key, value pairs preserving order
	body      string
	bodySize  int
	truncated bool
}

func parseRawBase64(
	raw string, includeHeaders, includeBody bool,
	bodyOffset, bodyLimit int,
) *parsedHTTP {
	if raw == "" {
		return nil
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil
	}

	result := &parsedHTTP{}
	parts := bytes.SplitN(decoded, []byte("\r\n\r\n"), 2)
	headerPart := parts[0]
	var bodyPart []byte
	if len(parts) > 1 {
		bodyPart = parts[1]
	}

	if includeHeaders {
		reader := bufio.NewReader(bytes.NewReader(headerPart))
		first, err := reader.ReadString('\n')
		if err == nil || err == io.EOF {
			result.firstLine = strings.TrimSpace(first)
		}
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				break
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			idx := strings.Index(line, ":")
			if idx > 0 {
				k := strings.TrimSpace(line[:idx])
				v := strings.TrimSpace(line[idx+1:])
				result.headers = append(
					result.headers, [2]string{k, v},
				)
			}
		}
	}

	result.bodySize = len(bodyPart)
	if includeBody && len(bodyPart) > 0 {
		if bodyOffset > 0 && bodyOffset < len(bodyPart) {
			bodyPart = bodyPart[bodyOffset:]
		} else if bodyOffset >= len(bodyPart) {
			bodyPart = nil
		}
		if bodyLimit > 0 && len(bodyPart) > bodyLimit {
			bodyPart = bodyPart[:bodyLimit]
			result.truncated = true
		}
		result.body = string(bodyPart)
	}

	return result
}

func fmtResp(p *parsedHTTP, allHeaders bool) string {
	if p == nil {
		return "(no response)"
	}

	status := ""
	if p.firstLine != "" {
		parts := strings.SplitN(p.firstLine, " ", 3)
		if len(parts) >= 2 {
			status = parts[1]
		}
	}

	ct := ""
	var hdrs []string
	for _, h := range p.headers {
		if strings.EqualFold(h[0], "content-type") {
			ct = strings.SplitN(h[1], ";", 2)[0]
		} else if allHeaders || secHeaders[strings.ToLower(h[0])] {
			hdrs = append(hdrs, h[0]+": "+h[1])
		}
	}

	var lines []string
	lines = append(lines,
		fmt.Sprintf("%s %s %dB", status, ct, p.bodySize),
	)
	lines = append(lines, hdrs...)
	lines = append(lines, "---")
	if p.body != "" {
		lines = append(lines, p.body)
	}
	if p.truncated {
		lines = append(lines,
			fmt.Sprintf("[...truncated, %dB total]", p.bodySize),
		)
	}
	return strings.Join(lines, "\n")
}

func fmtReq(p *parsedHTTP) string {
	if p == nil {
		return "(no request)"
	}
	var lines []string
	if p.firstLine != "" {
		lines = append(lines, p.firstLine)
	}
	for _, h := range p.headers {
		lines = append(lines, h[0]+": "+h[1])
	}
	if p.body != "" {
		lines = append(lines, "")
		lines = append(lines, p.body)
	}
	return strings.Join(lines, "\n")
}
