/*
Copyright 2025 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package cni

import (
	"encoding/json"
	"fmt"
	"slices"

	cnitypes "github.com/containernetworking/cni/pkg/types"
	cni100 "github.com/containernetworking/cni/pkg/types/100"
	resourcev1 "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/dynamic-resource-allocation/structured"
)

func addAllocatedDeviceStatusToResourceClaimStatus(claim *resourcev1.ResourceClaim, allocatedDeviceStatus resourcev1.AllocatedDeviceStatus) {
	for i, device := range claim.Status.Devices {
		if getDeviceID(device) == getDeviceID(allocatedDeviceStatus) {
			claim.Status.Devices[i] = allocatedDeviceStatus
			return
		}
	}

	claim.Status.Devices = append(claim.Status.Devices, allocatedDeviceStatus)
}

func removeAllocatedDeviceStatusFromResourceClaimStatus(claim *resourcev1.ResourceClaim, deviceRequestAllocationResult *resourcev1.DeviceRequestAllocationResult) {
	deviceID := structured.MakeDeviceID(deviceRequestAllocationResult.Driver, deviceRequestAllocationResult.Pool, deviceRequestAllocationResult.Device)
	sharedDeviceID := structured.MakeSharedDeviceID(deviceID, deviceRequestAllocationResult.ShareID)

	for i, device := range claim.Status.Devices {
		if getDeviceID(device) == sharedDeviceID {
			claim.Status.Devices = slices.Delete(claim.Status.Devices, i, i)
			return
		}
	}
}

func getDeviceID(device resourcev1.AllocatedDeviceStatus) structured.SharedDeviceID {
	deviceID := structured.MakeDeviceID(device.Driver, device.Pool, device.Device)
	return structured.MakeSharedDeviceID(deviceID, (*types.UID)(device.ShareID))
}

func buildAllocatedDeviceStatus(deviceRequestAllocationResult *resourcev1.DeviceRequestAllocationResult, result cnitypes.Result) (*resourcev1.AllocatedDeviceStatus, error) {
	resultBytes, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to json.Marshal result (%v): %v", result, err)
	}

	cniResult, err := cni100.NewResultFromResult(result)
	if err != nil {
		return nil, fmt.Errorf("failed to NewResultFromResult result (%v): %v", result, err)
	}

	data := runtime.RawExtension{
		Raw: resultBytes,
	}

	return &resourcev1.AllocatedDeviceStatus{
		Driver:      deviceRequestAllocationResult.Driver,
		Pool:        deviceRequestAllocationResult.Pool,
		Device:      deviceRequestAllocationResult.Device,
		ShareID:     (*string)(deviceRequestAllocationResult.ShareID),
		Data:        &data,
		NetworkData: cniResultToNetworkData(cniResult),
	}, nil
}

func cniResultToNetworkData(cniResult *cni100.Result) *resourcev1.NetworkDeviceData {
	networkData := &resourcev1.NetworkDeviceData{}

	for _, ip := range cniResult.IPs {
		networkData.IPs = append(networkData.IPs, ip.Address.String())
	}

	for _, ifs := range cniResult.Interfaces {
		// Only pod interfaces can have sandbox information
		if ifs.Sandbox != "" {
			networkData.InterfaceName = ifs.Name
			networkData.HardwareAddress = ifs.Mac
		}
	}

	return networkData
}
