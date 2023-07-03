/*
 * This file is part of the kiagnose project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright 2022 Red Hat, Inc.
 *
 */

package environment

import (
	"os"
	"strings"
)

func EnvToMap(rawEnv []string) map[string]string {
	const requiredElementsCount = 2

	env := map[string]string{}

	for _, entry := range rawEnv {
		splitKeyValue := strings.Split(entry, "=")
		if len(splitKeyValue) != requiredElementsCount {
			continue
		}

		env[splitKeyValue[0]] = splitKeyValue[1]
	}

	return env
}

func ReadNamespaceFile() (string, error) {
	const namespaceFile = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

	ns, err := os.ReadFile(namespaceFile)
	if err != nil {
		return "", err
	}

	return string(ns), nil
}
