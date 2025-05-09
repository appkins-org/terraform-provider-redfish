/*
Copyright (c) 2023-2024 Dell Inc., or its subsidiaries. All Rights Reserved.

Licensed under the Mozilla Public License Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://mozilla.org/MPL/2.0/


Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"terraform-provider-redfish/common"
	"terraform-provider-redfish/redfish/models"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/stmcginnis/gofish"
	redfishcommon "github.com/stmcginnis/gofish/common"
	"github.com/stmcginnis/gofish/redfish"
)

// Ensure the implementation satisfies the expected interfaces.
var (
	_ resource.Resource = &RedfishStorageVolumeResource{}
)

var volumeTypeMap = map[string]string{
	string(redfish.NonRedundantVolumeType):             "RAID0",
	string(redfish.MirroredVolumeType):                 "RAID1",
	string(redfish.StripedWithParityVolumeType):        "RAID5",
	string(redfish.SpannedMirrorsVolumeType):           "RAID10",
	string(redfish.SpannedStripesWithParityVolumeType): "RAID50",
}

const (
	defaultStorageVolumeResetTimeout  int64 = 120
	defaultStorageVolumeJobTimeout    int64 = 1200
	intervalStorageVolumeJobCheckTime int64 = 10
	maxCapacityBytes                  int64 = 1000000000
	maxVolumeNameLength               int   = 15
)

// NewRedfishStorageVolumeResource is a helper function to simplify the provider implementation.
func NewRedfishStorageVolumeResource() resource.Resource {
	return &RedfishStorageVolumeResource{}
}

// RedfishStorageVolumeResource is the resource implementation.
type RedfishStorageVolumeResource struct {
	p *redfishProvider
}

// Configure implements resource.ResourceWithConfigure
func (r *RedfishStorageVolumeResource) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	r.p = req.ProviderData.(*redfishProvider)
}

// Metadata returns the resource type name.
func (*RedfishStorageVolumeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "storage_volume"
}

// VolumeSchema defines the schema for the storage volume resource.
func VolumeSchema() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"capacity_bytes": schema.Int64Attribute{
			MarkdownDescription: "Capacity Bytes",
			Description:         "Capacity Bytes",
			Optional:            true,
			Validators: []validator.Int64{
				int64validator.AtLeast(maxCapacityBytes),
			},
		},
		"encrypted": schema.BoolAttribute{
			MarkdownDescription: "Encrypt the virtual disk, default is false. This flag is only supported on firmware levels 6 and above",
			Description:         "Encrypt the virtual disk, default is false. This flag is only supported on firmware levels 6 and above",
			Optional:            true,
			Computed:            true,
			Default:             booldefault.StaticBool(false),
		},
		"disk_cache_policy": schema.StringAttribute{
			MarkdownDescription: "Disk Cache Policy",
			Description:         "Disk Cache Policy",
			Optional:            true,
			Computed:            true,
			Default:             stringdefault.StaticString("Enabled"),
			Validators: []validator.String{
				stringvalidator.OneOf([]string{
					"Enabled",
					"Disabled",
				}...),
			},
		},
		"raid_type": schema.StringAttribute{
			MarkdownDescription: "Raid Type, Defaults to RAID0",
			Description:         "Raid Type, Defaults to RAID0.",
			Optional:            true,
			Computed:            true,
			Default:             stringdefault.StaticString("RAID0"),
			Validators: []validator.String{
				stringvalidator.OneOf([]string{
					"RAID0",
					"RAID1",
					"RAID5",
					"RAID6",
					"RAID10",
					"RAID50",
					"RAID60",
				}...),
			},
		},
		"drives": schema.ListAttribute{
			MarkdownDescription: "Drives",
			Description:         "Drives",
			Required:            true,
			ElementType:         types.StringType,
			Validators: []validator.List{
				listvalidator.SizeAtLeast(1),
			},
		},
		"id": schema.StringAttribute{
			MarkdownDescription: "ID of the storage volume resource",
			Description:         "ID of the storage volume resource",
			Computed:            true,
		},
		"optimum_io_size_bytes": schema.Int64Attribute{
			MarkdownDescription: "Optimum Io Size Bytes",
			Description:         "Optimum Io Size Bytes",
			Optional:            true,
		},
		"read_cache_policy": schema.StringAttribute{
			MarkdownDescription: "Read Cache Policy",
			Description:         "Read Cache Policy",
			Optional:            true,
			Computed:            true,
			Default:             stringdefault.StaticString(string(redfish.OffReadCachePolicyType)),
			Validators: []validator.String{
				stringvalidator.OneOf([]string{
					string(redfish.ReadAheadReadCachePolicyType),
					string(redfish.AdaptiveReadAheadReadCachePolicyType),
					string(redfish.OffReadCachePolicyType),
				}...),
			},
		},
		"reset_timeout": schema.Int64Attribute{
			MarkdownDescription: "Reset Timeout",
			Description:         "Reset Timeout",
			Optional:            true,
			Computed:            true,
			Default:             int64default.StaticInt64(defaultStorageVolumeResetTimeout),
		},
		"reset_type": schema.StringAttribute{
			MarkdownDescription: "Reset Type",
			Description:         "Reset Type",
			Optional:            true,
			Computed:            true,
			Default:             stringdefault.StaticString(string(redfish.ForceRestartResetType)),
			Validators: []validator.String{
				stringvalidator.OneOf([]string{
					string(redfish.ForceRestartResetType),
					string(redfish.GracefulRestartResetType),
					string(redfish.PowerCycleResetType),
				}...),
			},
		},
		"settings_apply_time": schema.StringAttribute{
			MarkdownDescription: "Settings Apply Time",
			Description:         "Settings Apply Time",
			Optional:            true,
			Computed:            true,
			Default:             stringdefault.StaticString(string(redfishcommon.ImmediateApplyTime)),
			Validators: []validator.String{
				stringvalidator.OneOf([]string{
					string(redfishcommon.ImmediateApplyTime),
					string(redfishcommon.OnResetApplyTime),
				}...),
			},
		},
		"storage_controller_id": schema.StringAttribute{
			MarkdownDescription: "Storage Controller ID",
			Description:         "Storage Controller ID",
			Required:            true,
		},
		"volume_job_timeout": schema.Int64Attribute{
			MarkdownDescription: "Volume Job Timeout",
			Description:         "Volume Job Timeout",
			Optional:            true,
			Computed:            true,
			Default:             int64default.StaticInt64(defaultStorageVolumeJobTimeout),
		},
		"volume_name": schema.StringAttribute{
			MarkdownDescription: "Volume Name",
			Description:         "Volume Name",
			Required:            true,
			Validators: []validator.String{
				stringvalidator.LengthAtLeast(1),
				stringvalidator.LengthAtMost(maxVolumeNameLength),
				stringvalidator.RegexMatches(
					regexp.MustCompile(`^[a-zA-Z0-9_-]*$`),
					"must only contain alphanumeric characters or '-' or '_'",
				),
			},
		},
		"volume_type": schema.StringAttribute{
			MarkdownDescription: "Volume Type",
			Description:         "Volume Type",
			Optional:            true,
			DeprecationMessage:  "Volume Type is deprecated and will be removed in a future release. Please use raid_type instead.",
			Validators: []validator.String{
				stringvalidator.OneOf([]string{
					string(redfish.NonRedundantVolumeType),
					string(redfish.MirroredVolumeType),
					string(redfish.StripedWithParityVolumeType),
					string(redfish.SpannedMirrorsVolumeType),
					string(redfish.SpannedStripesWithParityVolumeType),
				}...),
			},
		},
		"write_cache_policy": schema.StringAttribute{
			MarkdownDescription: "Write Cache Policy",
			Description:         "Write Cache Policy",
			Optional:            true,
			Computed:            true,
			Default:             stringdefault.StaticString(string(redfish.UnprotectedWriteBackWriteCachePolicyType)),
			Validators: []validator.String{
				stringvalidator.OneOf([]string{
					string(redfish.WriteThroughWriteCachePolicyType),
					string(redfish.ProtectedWriteBackWriteCachePolicyType),
					string(redfish.UnprotectedWriteBackWriteCachePolicyType),
				}...),
			},
		},
		"system_id": schema.StringAttribute{
			MarkdownDescription: "System ID of the system",
			Description:         "System ID of the system",
			Computed:            true,
			Optional:            true,
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.RequiresReplaceIfConfigured(),
			},
		},
	}
}

// Schema defines the schema for the resource.
func (*RedfishStorageVolumeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "This Terraform resource is used to configure virtual disks on the iDRAC Server." +
			" We can Create, Read, Update, Delete the virtual disks using this resource.",
		Description: "This Terraform resource is used to configure virtual disks on the iDRAC Server." +
			" We can Create, Read, Update, Delete the virtual disks using this resource.",
		Attributes: VolumeSchema(),
		Blocks:     RedfishServerResourceBlockMap(),
	}
}

// Create creates the resource and sets the initial Terraform state.
func (r *RedfishStorageVolumeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	tflog.Trace(ctx, "resource_RedfishStorageVolume create : Started")
	// Get Plan Data
	var plan models.RedfishStorageVolume
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	api, err := NewConfig(r.p, &plan.RedfishServer)
	if err != nil {
		resp.Diagnostics.AddError(ServiceErrorMsg, err.Error())
		return
	}
	service := api.Service
	defer api.Logout()

	diags = createRedfishStorageVolume(ctx, service, &plan)
	resp.Diagnostics.Append(diags...)

	tflog.Trace(ctx, "resource_RedfishStorageVolume create: updating state finished, saving ...")
	// Save into State
	if diags.HasError() {
		return
	}
	diags = resp.State.Set(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	tflog.Trace(ctx, "resource_RedfishStorageVolume create: finish")
}

// Read refreshes the Terraform state with the latest data.
func (r *RedfishStorageVolumeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	tflog.Trace(ctx, "resource_RedfishStorageVolume read: started")
	var state models.RedfishStorageVolume
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	api, err := NewConfig(r.p, &state.RedfishServer)
	if err != nil {
		resp.Diagnostics.AddError(ServiceErrorMsg, err.Error())
		return
	}
	service := api.Service
	defer api.Logout()

	diags, cleanup := readRedfishStorageVolume(service, &state)
	if cleanup {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(diags...)

	tflog.Trace(ctx, "resource_RedfishStorageVolume read: finished reading state")
	// Save into State
	if diags.HasError() {
		return
	}
	diags = resp.State.Set(ctx, &state)
	resp.Diagnostics.Append(diags...)
	tflog.Trace(ctx, "resource_RedfishStorageVolume read: finished")
}

// Update updates the resource and sets the updated Terraform state on success.
func (r *RedfishStorageVolumeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Get state Data
	tflog.Trace(ctx, "resource_RedfishStorageVolume update: started")
	var state, plan models.RedfishStorageVolume
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Get plan Data
	diags = req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !plan.Encrypted.ValueBool() && state.Encrypted.ValueBool() {
		resp.Diagnostics.AddError("Invalid Configuration.",
			"Cannot disable encryption, once a disk is encrypted it cannot be transformed back into an non-encrypted state.")
		return
	}
	api, err := NewConfig(r.p, &plan.RedfishServer)
	if err != nil {
		resp.Diagnostics.AddError(ServiceErrorMsg, err.Error())
		return
	}
	service := api.Service
	defer api.Logout()

	diags = updateRedfishStorageVolume(ctx, service, &plan, &state)
	resp.Diagnostics.Append(diags...)

	tflog.Trace(ctx, "resource_RedfishStorageVolume update: finished state update")
	// Save into State
	if diags.HasError() {
		return
	}
	diags = resp.State.Set(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	tflog.Trace(ctx, "resource_RedfishStorageVolume update: finished")
}

// Delete deletes the resource and removes the Terraform state on success.
func (r *RedfishStorageVolumeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	tflog.Trace(ctx, "resource_RedfishStorageVolume delete: started")
	// Get State Data
	var state models.RedfishStorageVolume
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	api, err := NewConfig(r.p, &state.RedfishServer)
	if err != nil {
		resp.Diagnostics.AddError(ServiceErrorMsg, err.Error())
		return
	}
	service := api.Service
	defer api.Logout()

	diags = deleteRedfishStorageVolume(ctx, service, &state)
	resp.Diagnostics.Append(diags...)
	if diags.HasError() {
		return
	}
	resp.State.RemoveResource(ctx)
	tflog.Trace(ctx, "resource_RedfishStorageVolume delete: finished")
}

// ImportState import state for existing volume
func (*RedfishStorageVolumeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	type creds struct {
		Username     string `json:"username"`
		Password     string `json:"password"`
		Endpoint     string `json:"endpoint"`
		SslInsecure  bool   `json:"ssl_insecure"`
		Id           string `json:"id"`
		SystemID     string `json:"system_id"`
		RedfishAlias string `json:"redfish_alias"`
	}

	var c creds
	err := json.Unmarshal([]byte(req.ID), &c)
	if err != nil {
		resp.Diagnostics.AddError("Error while unmarshalling id", err.Error())
	}

	server := models.RedfishServer{
		User:         types.StringValue(c.Username),
		Password:     types.StringValue(c.Password),
		Endpoint:     types.StringValue(c.Endpoint),
		SslInsecure:  types.BoolValue(c.SslInsecure),
		RedfishAlias: types.StringValue(c.RedfishAlias),
	}

	idAttrPath := path.Root("id")
	redfishServer := path.Root("redfish_server")
	resetTimeout := path.Root("reset_timeout")
	resetType := path.Root("reset_type")
	volumeJobTimeout := path.Root("volume_job_timeout")
	settingsApplyTime := path.Root("settings_apply_time")
	systemID := path.Root("system_id")
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, resetTimeout, defaultStorageVolumeResetTimeout)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, resetType, string(redfish.ForceRestartResetType))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, volumeJobTimeout, defaultStorageVolumeJobTimeout)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, settingsApplyTime, string(redfishcommon.ImmediateApplyTime))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, idAttrPath, c.Id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, redfishServer, []models.RedfishServer{server})...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, systemID, c.SystemID)...)
}

// nolint: revive
func createRedfishStorageVolume(ctx context.Context, service *gofish.Service, d *models.RedfishStorageVolume) diag.Diagnostics {
	var diags diag.Diagnostics
	// Lock the mutex to avoid race conditions with other resources
	redfishMutexKV.Lock(d.RedfishServer[0].Endpoint.ValueString())
	defer redfishMutexKV.Unlock(d.RedfishServer[0].Endpoint.ValueString())

	isGenerationSeventeenAndAbove, err := isServerGenerationSeventeenAndAbove(service)
	if err != nil {
		diags.AddError("Error retrieving the server generation", err.Error())
		return diags
	}

	storageID := d.StorageControllerID.ValueString()

	// Map from the deprecated volume type to raid type
	// If the raid_type is set, that will override the volume_type
	raidType := volumeTypeMap[d.VolumeType.ValueString()]
	if d.RaidType.ValueString() != "" {
		raidType = d.RaidType.ValueString()
	}
	volumeName := d.VolumeName.ValueString()
	optimumIOSizeBytes := int(d.OptimumIoSizeBytes.ValueInt64())
	capacityBytes := int(d.CapacityBytes.ValueInt64())
	readCachePolicy := d.ReadCachePolicy.ValueString()
	writeCachePolicy := d.WriteCachePolicy.ValueString()
	diskCachePolicy := d.DiskCachePolicy.ValueString()
	applyTime := d.SettingsApplyTime.ValueString()
	encrypted := d.Encrypted.ValueBool()
	volumeJobTimeout := int64(d.VolumeJobTimeout.ValueInt64())

	var driveNames []string
	diags.Append(d.Drives.ElementsAs(ctx, &driveNames, true)...)

	// Get storage
	storage, system, err := getStorage(service, d.SystemID.ValueString(), storageID)
	if err != nil {
		diags.AddError("Error when retreiving the Storage from the Redfish API", err.Error())
		return diags
	}

	d.SystemID = types.StringValue(system.ID)

	// Check if settings_apply_time is doable on this controller
	err = checkSettingsApplyTime(storage, applyTime)
	if err != nil {
		diags.AddError("Error while checking support for settings_apply_time", err.Error())
		return diags
	}

	// Get drives
	allStorageDrives, err := storage.Drives()
	if err != nil {
		diags.AddError("Error when getting the drives attached to controller", err.Error())
		return diags
	}
	drives, err := getDrives(allStorageDrives, driveNames)
	if err != nil {
		diags.AddError("Error when getting the drives", err.Error())
		return diags
	}

	newVolume := map[string]interface{}{
		"DisplayName":        volumeName,
		"Name":               volumeName,
		"ReadCachePolicy":    readCachePolicy,
		"WriteCachePolicy":   writeCachePolicy,
		"CapacityBytes":      capacityBytes,
		"OptimumIOSizeBytes": optimumIOSizeBytes,
		"RAIDType":           raidType,
		"Encrypted":          encrypted,
		"Oem": map[string]map[string]map[string]interface{}{
			"Dell": {
				"DellVolume": {
					"DiskCachePolicy": diskCachePolicy,
				},
			},
		},
		"@Redfish.OperationApplyTime": applyTime,
	}

	var listDrives []map[string]string
	for _, drive := range drives {
		storageDrive := make(map[string]string)
		storageDrive["@odata.id"] = drive.Entity.ODataID
		listDrives = append(listDrives, storageDrive)
	}

	// For 17G, have Drives as part of Links
	if isGenerationSeventeenAndAbove {
		newVolume["Links"] = map[string]interface{}{"Drives": listDrives}
	} else {
		newVolume["Drives"] = listDrives
	}

	// Create volume job
	jobID, err := createVolume(service, storage.ODataID, newVolume)
	if err != nil {
		diags.AddError("Error when creating the virtual disk on disk controller", err.Error())
		return diags
	}

	// Immediate or OnReset scenarios
	if applyTime == string(redfishcommon.OnResetApplyTime) { // OnReset case
		// Get reset_timeout and reset_type from schema
		resetType := d.ResetType.ValueString()
		resetTimeout := d.ResetTimeout.ValueInt64()

		// Reboot the server
		pOp := powerOperator{ctx, service, d.SystemID.ValueString()}
		_, err := pOp.PowerOperation(resetType, resetTimeout, intervalStorageVolumeJobCheckTime)
		if err != nil {
			diags.AddError(RedfishJobErrorMsg, err.Error())
			return diags
		}
	}

	// Wait for the job to finish
	err = common.WaitForTaskToFinish(service, jobID, intervalStorageVolumeJobCheckTime, volumeJobTimeout)
	if err != nil {
		diags.AddError(RedfishJobErrorMsg, err.Error())
		return diags
	}
	time.Sleep(60 * time.Second)

	// Get storage volumes
	volumes, err := storage.Volumes()
	if err != nil {
		diags.AddError("there was an issue when retrieving volumes", err.Error())
		return diags
	}
	volumeID, err := getVolumeID(volumes, volumeName)
	if err != nil {
		diags.AddError("Error. The volume ID with given volume name was not found", err.Error())
		return diags
	}

	d.ID = types.StringValue(volumeID)
	return diags
}

func readRedfishStorageVolume(service *gofish.Service, d *models.RedfishStorageVolume) (diags diag.Diagnostics, cleanup bool) {
	// Check if the volume exists
	volume, err := redfish.GetVolume(service.GetClient(), d.ID.ValueString())
	if err != nil {
		var redfishErr *redfishcommon.Error
		if !errors.As(err, &redfishErr) {
			diags.AddError("There was an error with the API", err.Error())
			return diags, false
		}
		if redfishErr.HTTPReturnedStatusCode == http.StatusNotFound {
			diags.AddError("Volume doesn't exist", "")
			return diags, true
		}
		diags.AddError("Status code", err.Error())
		return diags, false
	}

	d.CapacityBytes = types.Int64Value(int64(volume.CapacityBytes))
	d.ID = types.StringValue(volume.ODataID)
	d.OptimumIoSizeBytes = types.Int64Value(int64(volume.OptimumIOSizeBytes))
	d.ReadCachePolicy = types.StringValue(string(volume.ReadCachePolicy))
	d.VolumeName = types.StringValue(volume.Name)
	d.VolumeType = types.StringValue(string(volume.VolumeType))
	d.WriteCachePolicy = types.StringValue(string(volume.WriteCachePolicy))

	drives, _ := volume.Drives()
	drivesList := []attr.Value{}
	for _, drive := range drives {
		drivesList = append(drivesList, types.StringValue(drive.Name))
	}
	d.Drives, _ = types.ListValue(types.StringType, drivesList)

	/*
		- If it has jobID, if finished, get the volumeID
		Also never EVER trigger an update regarding disk properties for safety reasons
	*/

	return diags, false
}

func updateRedfishStorageVolume(ctx context.Context, service *gofish.Service,
	d *models.RedfishStorageVolume, state *models.RedfishStorageVolume,
) diag.Diagnostics {
	var diags diag.Diagnostics

	// Lock the mutex to avoid race conditions with other resources
	redfishMutexKV.Lock(d.RedfishServer[0].Endpoint.ValueString())
	defer redfishMutexKV.Unlock(d.RedfishServer[0].Endpoint.ValueString())

	// Get user config
	storageID := d.StorageControllerID.ValueString()
	volumeName := d.VolumeName.ValueString()
	readCachePolicy := d.ReadCachePolicy.ValueString()
	writeCachePolicy := d.WriteCachePolicy.ValueString()
	diskCachePolicy := d.DiskCachePolicy.ValueString()
	applyTime := d.SettingsApplyTime.ValueString()
	encrypted := d.Encrypted.ValueBool()

	var driveNames []string
	diags.Append(d.Drives.ElementsAs(ctx, &driveNames, true)...)

	volumeJobTimeout := d.ResetTimeout.ValueInt64()

	// Get storage
	storage, system, err := getStorage(service, d.SystemID.ValueString(), storageID)
	if err != nil {
		diags.AddError("Error when retreiving storage details from the Redfish API", err.Error())
		return diags
	}

	d.SystemID = types.StringValue(system.ID)
	// Check if settings_apply_time is doable on this controller
	err = checkSettingsApplyTime(storage, applyTime)
	if err != nil {
		diags.AddError("Error while checking support for settings_apply_time", err.Error())
		return diags
	}

	payload := map[string]interface{}{
		"ReadCachePolicy":  readCachePolicy,
		"WriteCachePolicy": writeCachePolicy,
		"DisplayName":      volumeName,
		"Encrypted":        encrypted,
		// This can be hard coded since the other values are deprecated, this is the only supported value
		"EncryptionTypes": []string{"NativeDriveEncryption"},
		"Oem": map[string]map[string]map[string]interface{}{
			"Dell": {
				"DellVolume": {
					"DiskCachePolicy": diskCachePolicy,
				},
			},
		},
		"Name": volumeName,
		"@Redfish.SettingsApplyTime": map[string]interface{}{
			"ApplyTime": applyTime,
		},
	}

	// Update volume job
	jobID, err := updateVolume(service, state.ID.ValueString(), payload)
	if err != nil {
		diags.AddError("Error when updating the virtual disk on disk controller", err.Error())
		return diags
	}

	// Immediate or OnReset scenarios
	if applyTime == string(redfishcommon.OnResetApplyTime) { // OnReset case
		resetType := d.ResetType.ValueString()
		resetTimeout := d.ResetTimeout.ValueInt64()

		// Reboot the server
		pOp := powerOperator{ctx, service, d.SystemID.ValueString()}
		_, err := pOp.PowerOperation(resetType, resetTimeout, intervalStorageVolumeJobCheckTime)
		if err != nil {
			diags.AddError(RedfishJobErrorMsg, err.Error())
			return diags
		}
	}

	// Wait for the job to finish
	err = common.WaitForTaskToFinish(service, jobID, intervalStorageVolumeJobCheckTime, volumeJobTimeout)
	if err != nil {
		diags.AddError(RedfishJobErrorMsg, err.Error())
		return diags
	}
	time.Sleep(60 * time.Second)

	// Get storage volumes
	volumes, err := storage.Volumes()
	if err != nil {
		diags.AddError("Issue when retrieving volumes", err.Error())
		return diags
	}
	volumeID, err := getVolumeID(volumes, volumeName)
	if err != nil {
		diags.AddError("The volume ID with given volume name was not found", err.Error())
		return diags
	}

	d.ID = types.StringValue(volumeID)
	return diags
}

func deleteRedfishStorageVolume(ctx context.Context, service *gofish.Service, d *models.RedfishStorageVolume) diag.Diagnostics {
	var diags diag.Diagnostics

	// Lock the mutex to avoid race conditions with other resources
	redfishMutexKV.Lock(d.RedfishServer[0].Endpoint.ValueString())
	defer redfishMutexKV.Unlock(d.RedfishServer[0].Endpoint.ValueString())

	// Get vars from schema
	applyTime := d.SettingsApplyTime.ValueString()
	volumeJobTimeout := d.VolumeJobTimeout.ValueInt64()

	jobID, err := deleteVolume(service, d.ID.ValueString())
	if err != nil {
		diags.AddError("Error when deleting volume", err.Error())
		return diags
	}

	if applyTime == string(redfishcommon.OnResetApplyTime) { // OnReset case
		// Get reset_timeout and reset_type from schema
		resetType := d.ResetType.ValueString()
		resetTimeout := d.ResetTimeout.ValueInt64()

		// sleep here to aviod the reboot action has impact on the volume delete job
		time.Sleep(30 * time.Second)

		// Reboot the server
		pOp := powerOperator{ctx, service, d.SystemID.ValueString()}
		_, err := pOp.PowerOperation(resetType, resetTimeout, intervalStorageVolumeJobCheckTime)
		if err != nil {
			diags.AddError(RedfishJobErrorMsg, err.Error())
			return diags
		}
	}

	// WAIT FOR VOLUME TO DELETE
	err = common.WaitForTaskToFinish(service, jobID, intervalStorageVolumeJobCheckTime, volumeJobTimeout)
	if err != nil {
		diags.AddError("Timeout reached when waiting for job to finish", err.Error())
		return diags
	}

	return diags
}

func getStorageController(storageControllers []*redfish.Storage, diskControllerID string) (*redfish.Storage, error) {
	for _, storage := range storageControllers {
		if storage.Entity.ID == diskControllerID {
			return storage, nil
		}
	}
	return nil, fmt.Errorf("couldn't find the storage controller %v", diskControllerID)
}

func deleteVolume(service *gofish.Service, volumeURI string) (jobID string, err error) {
	// TODO - Check if we can delete immediately or if we need to schedule a job
	res, err := service.GetClient().Delete(volumeURI)
	if err != nil {
		return "", fmt.Errorf("error while deleting the volume %s", volumeURI)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("the operation was not successful. Return code %d was different from 202 ACCEPTED", res.StatusCode)
	}
	jobID = res.Header.Get("Location")
	if len(jobID) == 0 {
		return "", fmt.Errorf("there was some error when retreiving the jobID")
	}
	return jobID, nil
}

func getDrives(drives []*redfish.Drive, driveNames []string) ([]*redfish.Drive, error) {
	drivesToReturn := []*redfish.Drive{}
	for _, v := range drives {
		for _, w := range driveNames {
			if v.Name == w {
				drivesToReturn = append(drivesToReturn, v)
			}
		}
	}
	if len(driveNames) != len(drivesToReturn) {
		return nil, fmt.Errorf("any of the drives you inserted doesn't exist")
	}
	return drivesToReturn, nil
}

func checkSettingsApplyTime(storage *redfish.Storage, applyTime string) error {
	operationApplyTimes, err := storage.GetOperationApplyTimeValues()
	if err != nil {
		return fmt.Errorf("couldn't retrieve operationApplyTimes from controller: %w", err)
	}
	if !checkOperationApplyTimes(applyTime, operationApplyTimes) {
		return fmt.Errorf("storage controller does not support settings_apply_time: %s", applyTime)
	}
	return nil
}

func getStorage(service *gofish.Service, sysID string, storageID string) (*redfish.Storage, *redfish.ComputerSystem, error) {
	system, err := getSystemResource(service, sysID)
	if err != nil {
		return nil, nil, fmt.Errorf("error when retreiving the Systems from the Redfish API: %w", err)
	}

	storageControllers, err := system.Storage()
	if err != nil {
		return nil, system, fmt.Errorf("error when retreiving the Storage from the Redfish API: %w", err)
	}

	storage, err := getStorageController(storageControllers, storageID)
	if err != nil {
		return nil, system, fmt.Errorf("error when getting the storage struct: %w", err)
	}
	return storage, system, nil
}

/*
createVolume creates a virtualdisk on a disk controller by using the redfish API
*/
func createVolume(service *gofish.Service,
	storageLink string,
	newVolume map[string]interface{},
) (jobID string, err error) {
	volumesURL := fmt.Sprintf("%v/Volumes", storageLink)

	res, err := service.GetClient().Post(volumesURL, newVolume)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("the query was unsucessfull")
	}
	jobID = res.Header.Get("Location")
	if len(jobID) == 0 {
		return "", fmt.Errorf("there was some error when retreiving the jobID")
	}
	return jobID, nil
}

func updateVolume(service *gofish.Service,
	storageLink string,
	payload map[string]interface{},
) (jobID string, err error) {
	volumesURL := fmt.Sprintf("%v/Settings", storageLink)

	res, err := service.GetClient().Patch(volumesURL, payload)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("the query was unsucessfull")
	}
	jobID = res.Header.Get("Location")
	if len(jobID) == 0 {
		return "", fmt.Errorf("there was some error when retreiving the jobID")
	}
	return jobID, nil
}

func getVolumeID(volumes []*redfish.Volume, volumeName string) (volumeLink string, err error) {
	for _, v := range volumes {
		if v.Name == volumeName {
			volumeLink = v.ODataID
			return volumeLink, nil
		}
	}
	return "", fmt.Errorf("couldn't find a volume with the provided name: %s", volumeName)
}

func checkOperationApplyTimes(optionToCheck string, storageOperationApplyTimes []redfishcommon.OperationApplyTime) (result bool) {
	for _, v := range storageOperationApplyTimes {
		if optionToCheck == string(v) {
			return true
		}
	}
	return false
}
