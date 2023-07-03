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

import (
	"errors"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/kiagnose/kiagnose/kiagnose/configmap"
	"github.com/kiagnose/kiagnose/kiagnose/types"
)

var (
	ErrConfigMapDataIsNil      = errors.New("configMap Data field is nil")
	ErrConfigMapIsAlreadyInUse = errors.New("configMap is already in use")
)

type Config struct {
	ConfigMapNamespace string
	ConfigMapName      string
	PodName            string
	PodUID             string
	UID                string
	Timeout            time.Duration
	Params             map[string]string
}

type configMapSettings struct {
	UID     string
	Timeout time.Duration
	Params  map[string]string
}

func Read(client kubernetes.Interface, rawEnv map[string]string) (Config, error) {
	env := newEnvironment(rawEnv)

	if err := env.Validate(); err != nil {
		return Config{}, err
	}

	cmSettings, err := readFromConfigMap(client, env.ConfigMapNamespace, env.ConfigMapName)
	if err != nil {
		return Config{}, err
	}

	return Config{
		ConfigMapNamespace: env.ConfigMapNamespace,
		ConfigMapName:      env.ConfigMapName,
		PodName:            env.PodName,
		PodUID:             env.PodUID,
		UID:                cmSettings.UID,
		Timeout:            cmSettings.Timeout,
		Params:             cmSettings.Params,
	}, nil
}

func readFromConfigMap(client kubernetes.Interface, configMapNamespace, configMapName string) (configMapSettings, error) {
	configMap, err := configmap.Get(client, configMapNamespace, configMapName)
	if err != nil {
		return configMapSettings{}, err
	}

	if configMap.Data == nil {
		return configMapSettings{}, ErrConfigMapDataIsNil
	}

	if isConfigMapAlreadyInUse(configMap.Data) {
		return configMapSettings{}, ErrConfigMapIsAlreadyInUse
	}

	parser := newConfigMapParser(configMap.Data)
	err = parser.Parse()
	if err != nil {
		return configMapSettings{}, err
	}

	return configMapSettings{
		UID:     string(configMap.UID),
		Timeout: parser.Timeout,
		Params:  parser.Params,
	}, nil
}

func isConfigMapAlreadyInUse(data map[string]string) bool {
	_, exists := data[types.StartTimestampKey]
	return exists
}
