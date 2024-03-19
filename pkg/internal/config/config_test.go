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

package config_test

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	kconfig "github.com/kiagnose/kiagnose/kiagnose/config"
	kconfigmap "github.com/kiagnose/kiagnose/kiagnose/configmap"
	"github.com/kiagnose/kiagnose/kiagnose/types"

	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/config"

	"github.com/stretchr/testify/assert"
)

const (
	testNamespace     = "target-ns"
	testConfigMapName = "storage-checkup-config"
	testPodName       = "pod"
	testPodUID        = "uid"
)

var testEnv = map[string]string{
	kconfig.ConfigMapNamespaceEnvVarName: testNamespace,
	kconfig.ConfigMapNameEnvVarName:      testConfigMapName,
	kconfig.PodNameEnvVarName:            testPodName,
	kconfig.PodUIDEnvVarName:             testPodUID,
}

func TestInitConfigMapShouldFailWhenNoConfigMap(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	_, err := config.ReadWithDefaults(fakeClient, testNamespace, testEnv)
	assert.ErrorContains(t, err, "not found")
}

func TestInitConfigMapShouldFailWhenNoEnvVars(t *testing.T) {
	fakeClient := fake.NewSimpleClientset(newConfigMap())
	emptyEnv := map[string]string{}
	_, err := config.ReadWithDefaults(fakeClient, testNamespace, emptyEnv)
	assert.ErrorContains(t, err, "no environment variables")
}

func TestInitConfigMapShouldSucceed(t *testing.T) {
	fakeClient := fake.NewSimpleClientset(newConfigMap())
	_, err := config.ReadWithDefaults(fakeClient, testNamespace, testEnv)
	assert.NoError(t, err)

	cm, err := kconfigmap.Get(fakeClient, testNamespace, testConfigMapName)
	assert.NoError(t, err)
	assert.NotNil(t, cm.Labels)
	assert.Equal(t, "kubevirt-vm-storage", cm.Labels["kiagnose/checkup-type"])
	assert.NotNil(t, cm.Data)
	assert.Equal(t, "10m", cm.Data[types.TimeoutKey])
}

func TestInitConfigMapShouldNotUpdateTimeout(t *testing.T) {
	cm := newConfigMap()
	cm.Data[types.TimeoutKey] = "15m"
	fakeClient := fake.NewSimpleClientset(cm)
	_, err := config.ReadWithDefaults(fakeClient, testNamespace, testEnv)
	assert.NoError(t, err)

	cm, err = kconfigmap.Get(fakeClient, testNamespace, testConfigMapName)
	assert.NoError(t, err)
	assert.NotNil(t, cm.Labels)
	assert.Equal(t, "kubevirt-vm-storage", cm.Labels["kiagnose/checkup-type"])
	assert.NotNil(t, cm.Data)
	assert.Equal(t, "15m", cm.Data[types.TimeoutKey])
}

func newConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testConfigMapName,
			Namespace: testNamespace,
		},
		Data: map[string]string{},
	}
}
