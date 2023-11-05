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
	kconfig "github.com/kiagnose/kiagnose/kiagnose/config"
	kconfigmap "github.com/kiagnose/kiagnose/kiagnose/configmap"

	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/client"
)

// FIXME: pass something here - maybe golden image ns?
type Config struct {
}

func New(c *client.Client, baseConfig kconfig.Config) (Config, error) {
	newConfig := Config{}
	err := signConfigMap(c, baseConfig)
	return newConfig, err
}

func signConfigMap(c *client.Client, baseConfig kconfig.Config) error {
	cm, err := kconfigmap.Get(c, baseConfig.ConfigMapNamespace, baseConfig.ConfigMapName)
	if err != nil {
		return err
	}
	if cm.Labels == nil {
		cm.Labels = map[string]string{}
	}
	cm.Labels["kiagnose/checkup-type"] = "kubevirt-vm-storage"
	_, err = kconfigmap.Update(c, cm)

	return err
}
