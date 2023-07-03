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

package config

import "fmt"

type environment struct {
	ConfigMapNamespace string
	ConfigMapName      string
	PodName            string
	PodUID             string
}

const (
	ConfigMapNamespaceEnvVarName = "CONFIGMAP_NAMESPACE"
	ConfigMapNameEnvVarName      = "CONFIGMAP_NAME"
	PodNameEnvVarName            = "HOSTNAME"
	PodUIDEnvVarName             = "POD_UID"
)

var (
	ErrMissingConfigMapNamespace = fmt.Errorf("missing required environment variable: %q", ConfigMapNamespaceEnvVarName)
	ErrMissingConfigMapName      = fmt.Errorf("missing required environment variable: %q", ConfigMapNameEnvVarName)
	ErrMissingPodName            = fmt.Errorf("missing required environment variable: %q", PodNameEnvVarName)
)

func newEnvironment(rawEnv map[string]string) environment {
	return environment{
		ConfigMapNamespace: rawEnv[ConfigMapNamespaceEnvVarName],
		ConfigMapName:      rawEnv[ConfigMapNameEnvVarName],
		PodName:            rawEnv[PodNameEnvVarName],
		PodUID:             rawEnv[PodUIDEnvVarName],
	}
}

func (e environment) Validate() error {
	if e.ConfigMapNamespace == "" {
		return ErrMissingConfigMapNamespace
	}

	if e.ConfigMapName == "" {
		return ErrMissingConfigMapName
	}

	if e.PodName == "" {
		return ErrMissingPodName
	}

	// PodUID field is optional, thus not validated

	return nil
}
