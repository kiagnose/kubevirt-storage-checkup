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
	"context"
	"errors"
	"strconv"
	"time"

	kconfig "github.com/kiagnose/kiagnose/kiagnose/config"
	kconfigmap "github.com/kiagnose/kiagnose/kiagnose/configmap"
	"github.com/kiagnose/kiagnose/kiagnose/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	StorageClassParamName = "storageClass"
	VMITimeoutParamName   = "vmiTimeout"
	NumOfVMsParamName     = "numOfVMs"
	SkipTeardownParamName = "skipTeardown"
)

// SkipTeardownMode defines the possible modes for skipping teardown.
type SkipTeardownMode string

const (
	SkipTeardownOnFailure SkipTeardownMode = "onfailure"
	SkipTeardownAlways    SkipTeardownMode = "always"
	SkipTeardownNever     SkipTeardownMode = "never"
)

const (
	VMITimeoutDefault = 3 * time.Minute
	NumOfVMsDefault   = 10
)

var (
	ErrInvalidVMITimeout       = errors.New("invalid VMI timeout")
	ErrInvalidNumOfVMs         = errors.New("invalid number of VMIs")
	ErrInvalidSkipTeardownMode = errors.New("invalid skip teardown mode")
)

type Config struct {
	PodName      string
	PodUID       string
	StorageClass string
	VMITimeout   time.Duration
	NumOfVMs     int
	SkipTeardown SkipTeardownMode
}

func New(baseConfig kconfig.Config) (Config, error) {
	newConfig := Config{
		PodName:    baseConfig.PodName,
		PodUID:     baseConfig.PodUID,
		VMITimeout: VMITimeoutDefault,
		NumOfVMs:   NumOfVMsDefault,
	}

	return setOptionalParams(baseConfig, newConfig)
}

func setOptionalParams(baseConfig kconfig.Config, newConfig Config) (Config, error) {
	var err error

	if sc, exists := baseConfig.Params[StorageClassParamName]; exists {
		newConfig.StorageClass = sc
	}

	if rawVal, exists := baseConfig.Params[VMITimeoutParamName]; exists && rawVal != "" {
		newConfig.VMITimeout, err = time.ParseDuration(rawVal)
		if err != nil {
			return Config{}, ErrInvalidVMITimeout
		}
	}

	if rawVal, exists := baseConfig.Params[NumOfVMsParamName]; exists && rawVal != "" {
		numOfVMs, err := strconv.Atoi(rawVal)
		if err != nil || numOfVMs < 1 || numOfVMs > 100 {
			return Config{}, ErrInvalidNumOfVMs
		}
		newConfig.NumOfVMs = numOfVMs
	}

	if rawVal, exists := baseConfig.Params[SkipTeardownParamName]; exists && rawVal != "" {
		switch SkipTeardownMode(rawVal) {
		case SkipTeardownOnFailure, SkipTeardownAlways, SkipTeardownNever:
			newConfig.SkipTeardown = SkipTeardownMode(rawVal)
		default:
			skip, err := strconv.ParseBool(rawVal)
			if err != nil {
				return Config{}, ErrInvalidSkipTeardownMode
			}
			if skip {
				newConfig.SkipTeardown = SkipTeardownAlways
			}
		}
	}

	return newConfig, nil
}

// ReadWithDefaults inits the configmap with defaults where needed before reading it by kiagnose config infra
func ReadWithDefaults(client kubernetes.Interface, namespace string, rawEnv map[string]string) (kconfig.Config, error) {
	cmNamespace := rawEnv[kconfig.ConfigMapNamespaceEnvVarName]
	cmName := rawEnv[kconfig.ConfigMapNameEnvVarName]
	if cmNamespace == "" || cmName == "" {
		return kconfig.Config{}, errors.New("no environment variables set for configmap namespace and name")
	}

	podName := rawEnv[kconfig.PodNameEnvVarName]
	podUID := rawEnv[kconfig.PodUIDEnvVarName]
	if podName != "" && podUID == "" {
		pod, err := client.CoreV1().Pods(namespace).Get(context.Background(), podName, metav1.GetOptions{})
		if err != nil {
			return kconfig.Config{}, err
		}
		rawEnv[kconfig.PodUIDEnvVarName] = string(pod.UID)
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
