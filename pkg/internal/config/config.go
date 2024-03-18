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
 * Copyright 2023 Red Hat, Inc.
 *
 */

package config

import (
	"errors"

	kconfig "github.com/kiagnose/kiagnose/kiagnose/config"
	kconfigmap "github.com/kiagnose/kiagnose/kiagnose/configmap"
	"github.com/kiagnose/kiagnose/kiagnose/types"
	"k8s.io/client-go/kubernetes"
)

// FIXME: pass something here - maybe golden image ns?
type Config struct {
}

func New(baseConfig kconfig.Config) (Config, error) {
	newConfig := Config{}
	return newConfig, nil
}

// ReadWithDefaults inits the configmap with defaults where needed before reading it by kiagnose config infra
func ReadWithDefaults(client kubernetes.Interface, rawEnv map[string]string) (kconfig.Config, error) {
	cmNamespace := rawEnv[kconfig.ConfigMapNamespaceEnvVarName]
	cmName := rawEnv[kconfig.ConfigMapNameEnvVarName]
	if cmNamespace == "" || cmName == "" {
		return kconfig.Config{}, errors.New("no environment variables set for configmap namespace and name")
	}

	cm, err := kconfigmap.Get(client, cmNamespace, cmName)
	if err != nil {
		return kconfig.Config{}, err
	}

	if cm.Labels == nil {
		cm.Labels = map[string]string{}
	}
	cm.Labels["kiagnose/checkup-type"] = "kubevirt-vm-storage"

	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	_, exists := cm.Data[types.TimeoutKey]
	if !exists {
		cm.Data[types.TimeoutKey] = "10m"
	}

	if _, err = kconfigmap.Update(client, cm); err != nil {
		return kconfig.Config{}, err
	}

	return kconfig.Read(client, rawEnv)
}
