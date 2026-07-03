package webservice

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
)

// mustNewRequest 构造一个测试请求（POST + 给定 body）。
func mustNewRequest(method, path, body string) *http.Request {
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, path, r)
	if err != nil {
		panic(err)
	}
	return req
}

// mustServe 用 httptest 跑一个 handler 并返回 ResponseRecorder。
func mustServe(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}
