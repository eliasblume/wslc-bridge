/**********************************************************************
 * Copyright (C) 2026 Red Hat, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * SPDX-License-Identifier: Apache-2.0
 **********************************************************************/

package guest

import (
	"net/http"
	"net/url"
	"time"
)

// Client sends HTTP requests to the guest Docker API.
type Client interface {
	Do(request *http.Request) (*http.Response, error)
	CloseIdleConnections()
}

var _ Client = (*http.Client)(nil)

// NewClient returns a Client that reaches the guest Docker API over a WSLC
// stdio session.
func NewClient(wslcPath string) Client {
	dialer := NewStdioDialer(wslcPath)
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		DisableCompression:    true,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 0,
		ForceAttemptHTTP2:     false,
		Proxy:                 func(*http.Request) (*url.URL, error) { return nil, nil },
	}
	return &http.Client{Transport: transport, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
}
