/*
Copyright (c) 2024 Dell Inc., or its subsidiaries. All Rights Reserved.

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

resource "terraform_data" "trigger_by_timestamp" {
  input = timestamp()
}

resource "redfish_idrac_server_configuration_profile_import" "share_type_local" {
  for_each = var.rack1

  redfish_server {
    # Alias name for server BMCs. The key in provider's `redfish_servers` map
    # `redfish_alias` is used to align with enhancements to password management.
    # When using redfish_alias, provider's `redfish_servers` is required.
    redfish_alias = each.key

    user         = each.value.user
    password     = each.value.password
    endpoint     = each.value.endpoint
    ssl_insecure = each.value.ssl_insecure
  }

  share_parameters = {
    filename   = "demo_local.xml"
    target     = ["NIC"]
    share_type = "LOCAL"
  }

  lifecycle {
    replace_triggered_by = [terraform_data.trigger_by_timestamp]
  }
}

resource "redfish_idrac_server_configuration_profile_import" "share_type_nfs" {
  for_each = var.rack1

  redfish_server {
    # Alias name for server BMCs. The key in provider's `redfish_servers` map
    # `redfish_alias` is used to align with enhancements to password management.
    # When using redfish_alias, provider's `redfish_servers` is required.
    redfish_alias = each.key

    user         = each.value.user
    password     = each.value.password
    endpoint     = each.value.endpoint
    ssl_insecure = each.value.ssl_insecure
  }

  share_parameters = {
    filename   = "demo_nfs.xml"
    target     = ["NIC"]
    share_type = "NFS"
    ip_address = "10.0.0.01"
    share_name = "/dell/terraform-idrac-nfs"
  }

  lifecycle {
    replace_triggered_by = [terraform_data.trigger_by_timestamp]
  }
}

resource "redfish_idrac_server_configuration_profile_import" "share_type_cifs" {
  for_each = var.rack1

  redfish_server {
    # Alias name for server BMCs. The key in provider's `redfish_servers` map
    # `redfish_alias` is used to align with enhancements to password management.
    # When using redfish_alias, provider's `redfish_servers` is required.
    redfish_alias = each.key

    user         = each.value.user
    password     = each.value.password
    endpoint     = each.value.endpoint
    ssl_insecure = each.value.ssl_insecure
  }

  share_parameters = {
    filename   = "demo_cifs.xml"
    target     = ["NIC"]
    share_type = "CIFS"
    ip_address = "10.0.0.02"
    share_name = "/dell/terraform-idrac-nfs"
    username   = var.cifs_username
    password   = var.cifs_password
  }

  lifecycle {
    replace_triggered_by = [terraform_data.trigger_by_timestamp]
  }
}

resource "redfish_idrac_server_configuration_profile_import" "share_type_https" {
  for_each = var.rack1

  redfish_server {
    # Alias name for server BMCs. The key in provider's `redfish_servers` map
    # `redfish_alias` is used to align with enhancements to password management.
    # When using redfish_alias, provider's `redfish_servers` is required.
    redfish_alias = each.key

    user         = each.value.user
    password     = each.value.password
    endpoint     = each.value.endpoint
    ssl_insecure = each.value.ssl_insecure
  }

  share_parameters = {
    filename    = "demo_https.xml"
    target      = ["NIC"]
    share_type  = "HTTPS"
    ip_address  = "10.0.0.03"
    port_number = 443
  }

  lifecycle {
    replace_triggered_by = [terraform_data.trigger_by_timestamp]
  }
}

resource "redfish_idrac_server_configuration_profile_import" "share_type_http" {
  for_each = var.rack1

  redfish_server {
    # Alias name for server BMCs. The key in provider's `redfish_servers` map
    # `redfish_alias` is used to align with enhancements to password management.
    # When using redfish_alias, provider's `redfish_servers` is required.
    redfish_alias = each.key

    user         = each.value.user
    password     = each.value.password
    endpoint     = each.value.endpoint
    ssl_insecure = each.value.ssl_insecure
  }

  share_parameters = {
    filename      = "demo_http.xml"
    target        = ["NIC"]
    share_type    = "HTTP"
    ip_address    = "10.0.0.04"
    port_number   = 80
    proxy_support = true
    proxy_server  = "10.0.0.05"
    proxy_port    = 5000
  }

  lifecycle {
    replace_triggered_by = [terraform_data.trigger_by_timestamp]
  }
}