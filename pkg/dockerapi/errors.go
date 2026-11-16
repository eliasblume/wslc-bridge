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

package dockerapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"wslc-bridge/pkg/process"
)

var errUnsupportedAPIVersion = errors.New("Docker API versions older than 1.24 are not supported")

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func commandStatus(err error) int {
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}
	var commandError *process.CommandError
	if errors.As(err, &commandError) {
		message := strings.ToLower(commandError.Stderr)
		if strings.Contains(message, "not found") || strings.Contains(message, "no such") {
			return http.StatusNotFound
		}
		if strings.Contains(message, "already") || strings.Contains(message, "conflict") || strings.Contains(message, "running") {
			return http.StatusConflict
		}
		return http.StatusBadGateway
	}
	return http.StatusBadGateway
}
