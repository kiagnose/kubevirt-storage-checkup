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

package k8s

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"

	storagev1 "k8s.io/api/storage/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/checkup/shared"
	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/config"
	"github.com/kiagnose/kubevirt-storage-checkup/pkg/internal/status"
)

type K8sStorageClient interface {
	ListStorageClasses(ctx context.Context) (*storagev1.StorageClassList, error)
	GetCSIDriver(ctx context.Context, name string) (*storagev1.CSIDriver, error)
}

const (
	ErrEmptyProvisioner               = "StorageClass %q has an empty provisioner"
	ErrPersistentLifecycleUnsupported = "CSIDriver %q does not declare Persistent volume lifecycle support"
	MessageStorageClassFound          = "StorageClass %q found; provisioner %q"
	MessageCSIDriverFound             = "CSIDriver %q found; Persistent lifecycle declared or defaulted"
	MessageSkipInvalidStorageClass    = "Skipped - StorageClass discovery did not succeed"
	MessageSkipStaticProvisioner      = "Skipped - selected StorageClass uses static provisioning"
	MessageSkipLegacyProvisioner      = "Skipped - CSI discovery for legacy provisioner %q is unsupported; " +
		"CSI migration was not evaluated"
	MessageCSIDriverNotFound = "Warning - no CSIDriver object found for provisioner %q; " +
		"the provisioner may be non-CSI or its CSI metadata may be missing"
	MessageNotRun = "Not run"
)

type Checkup struct {
	client        K8sStorageClient
	namespace     string
	state         *shared.State
	checkupConfig config.Config
	results       status.Results
}

func New(client K8sStorageClient, namespace string, state *shared.State, checkupConfig config.Config) *Checkup {
	return &Checkup{
		client:        client,
		namespace:     namespace,
		state:         state,
		checkupConfig: checkupConfig,
	}
}

func (c *Checkup) Setup(ctx context.Context) error {
	return ctx.Err()
}

func (c *Checkup) Run(ctx context.Context) error {
	errStr := ""

	if err := c.checkStorageClassAndCSIDriver(ctx, &errStr); err != nil {
		return err
	}

	if errStr != "" {
		return errors.New(errStr)
	}

	return nil
}

func (c *Checkup) checkStorageClassAndCSIDriver(ctx context.Context, errStr *string) error {
	log.Print("checkStorageClassAndCSIDriver")

	c.state.StorageClasses = nil
	c.state.SelectedStorageClass = nil
	c.state.DefaultStorageClass = ""
	c.state.CSIDriver = nil

	c.results.SelectedStorageClass = c.checkupConfig.StorageClass
	c.results.StorageClassProvisioner = ""
	c.results.DefaultStorageClass = ""
	c.results.StorageClassDiscovery = MessageNotRun
	c.results.CSIDriverDiscovery = MessageNotRun

	if err := ctx.Err(); err != nil {
		return err
	}

	sc, err := c.checkStorageClass(ctx, errStr)
	if err != nil {
		return err
	}

	return c.checkCSIDriver(ctx, sc, errStr)
}

func (c *Checkup) checkStorageClass(ctx context.Context, errStr *string) (*storagev1.StorageClass, error) {
	log.Print("checkStorageClass")

	scs, err := c.client.ListStorageClasses(ctx)
	if err != nil {
		wrapped := fmt.Errorf("list StorageClasses: %w", err)
		c.results.StorageClassDiscovery = wrapped.Error()
		return nil, wrapped
	}
	c.state.StorageClasses = scs

	defaultSC, defaultErr := shared.FindDefaultStorageClass(scs)
	if defaultErr == nil && defaultSC != nil {
		c.state.DefaultStorageClass = defaultSC.Name
		c.results.DefaultStorageClass = defaultSC.Name
	}

	var sc *storagev1.StorageClass
	if name := c.checkupConfig.StorageClass; name != "" {
		sc, err = shared.FindStorageClass(scs, name)
	} else {
		sc, err = defaultSC, defaultErr
		if defaultErr != nil {
			c.results.DefaultStorageClass = defaultErr.Error()
		}
	}

	if err != nil {
		c.reportFailure(
			&c.results.StorageClassDiscovery,
			errStr,
			err.Error(),
		)
		return nil, nil
	}

	if sc == nil {
		c.results.DefaultStorageClass = shared.ErrNoDefaultStorageClass
		c.reportFailure(
			&c.results.StorageClassDiscovery,
			errStr,
			shared.ErrNoDefaultStorageClass,
		)
		return nil, nil
	}

	c.state.SelectedStorageClass = sc
	c.results.SelectedStorageClass = sc.Name
	c.results.StorageClassProvisioner = sc.Provisioner

	if strings.TrimSpace(sc.Provisioner) == "" {
		c.reportFailure(
			&c.results.StorageClassDiscovery,
			errStr,
			fmt.Sprintf(ErrEmptyProvisioner, sc.Name),
		)
		return nil, nil
	}

	c.results.StorageClassDiscovery = fmt.Sprintf(
		MessageStorageClassFound,
		sc.Name,
		sc.Provisioner,
	)
	log.Print(c.results.StorageClassDiscovery)

	return sc, nil
}

func (c *Checkup) checkCSIDriver(ctx context.Context, sc *storagev1.StorageClass, errStr *string) error {
	log.Print("checkCSIDriver")

	if sc == nil {
		c.results.CSIDriverDiscovery = MessageSkipInvalidStorageClass
		return nil
	}

	if sc.Provisioner == "kubernetes.io/no-provisioner" {
		c.results.CSIDriverDiscovery = MessageSkipStaticProvisioner
		return nil
	}

	if strings.HasPrefix(sc.Provisioner, "kubernetes.io/") {
		c.results.CSIDriverDiscovery = fmt.Sprintf(
			MessageSkipLegacyProvisioner,
			sc.Provisioner,
		)
		return nil
	}

	driver, err := c.client.GetCSIDriver(ctx, sc.Provisioner)

	if k8serrors.IsNotFound(err) {
		// Missing discovery metadata alone does not prove driver failure.
		c.results.CSIDriverDiscovery = fmt.Sprintf(
			MessageCSIDriverNotFound,
			sc.Provisioner,
		)
		log.Print(c.results.CSIDriverDiscovery)
		return nil
	}

	if err != nil {
		wrapped := fmt.Errorf("get CSIDriver %q: %w", sc.Provisioner, err)
		c.results.CSIDriverDiscovery = wrapped.Error()
		return wrapped
	}

	c.state.CSIDriver = driver
	modes := driver.Spec.VolumeLifecycleModes

	// An empty list defaults to Persistent.
	if len(modes) > 0 &&
		!slices.Contains(modes, storagev1.VolumeLifecyclePersistent) {
		c.reportFailure(
			&c.results.CSIDriverDiscovery,
			errStr,
			fmt.Sprintf(ErrPersistentLifecycleUnsupported, driver.Name),
		)
		return nil
	}

	c.results.CSIDriverDiscovery = fmt.Sprintf(
		MessageCSIDriverFound,
		driver.Name,
	)
	log.Print(c.results.CSIDriverDiscovery)

	return nil
}

func (c *Checkup) reportFailure(result, errStr *string, message string) {
	log.Print(message)
	*result = message
	appendSep(errStr, message)
}

func (c *Checkup) Teardown(_ context.Context) error {
	return nil
}

func (c *Checkup) Results() status.Results {
	return c.results
}

func (c *Checkup) Config() config.Config {
	return c.checkupConfig
}

func appendSep(result *string, message string) {
	if *result != "" {
		*result += "\n"
	}
	*result += message
}
