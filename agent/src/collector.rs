use sysinfo::{Disks, Networks, System};

use crate::protocol::{FilesystemMetric, HostSnapshot, InterfaceMetric};

pub trait HostCollector {
    fn collect(&mut self) -> HostSnapshot;
}

pub struct SysinfoHostCollector {
    system: System,
    disks: Disks,
    networks: Networks,
}

impl SysinfoHostCollector {
    pub fn new() -> Self {
        let mut system = System::new_all();
        system.refresh_cpu_usage();
        Self {
            system,
            disks: Disks::new_with_refreshed_list(),
            networks: Networks::new_with_refreshed_list(),
        }
    }
}

impl Default for SysinfoHostCollector {
    fn default() -> Self {
        Self::new()
    }
}

impl HostCollector for SysinfoHostCollector {
    fn collect(&mut self) -> HostSnapshot {
        self.system.refresh_memory();
        self.system.refresh_cpu_usage();
        self.disks.refresh(true);
        self.networks.refresh(true);

        let operating_system = match (System::name(), System::os_version()) {
            (Some(name), Some(version)) => Some(format!("{name} {version}")),
            (Some(name), None) => Some(name),
            (None, Some(version)) => Some(version),
            (None, None) => None,
        };

        let filesystems = self
            .disks
            .list()
            .iter()
            .map(|disk| FilesystemMetric {
                mount_point: disk.mount_point().to_string_lossy().into_owned(),
                total_bytes: non_zero(disk.total_space()),
                available_bytes: non_zero(disk.available_space()),
            })
            .collect();

        let interfaces = self
            .networks
            .iter()
            .map(|(name, network)| {
                let mac = network.mac_address().to_string();
                InterfaceMetric {
                    name: name.clone(),
                    mac_address: (mac != "00:00:00:00:00:00").then_some(mac),
                    addresses: network
                        .ip_networks()
                        .iter()
                        .map(|address| address.addr.to_string())
                        .collect(),
                    received_bytes: Some(network.total_received()),
                    transmitted_bytes: Some(network.total_transmitted()),
                }
            })
            .collect();

        HostSnapshot {
            hostname: System::host_name(),
            operating_system,
            kernel_version: System::kernel_version(),
            cpu_usage_percent: Some(self.system.global_cpu_usage().clamp(0.0, 100.0)),
            memory_used_bytes: non_zero(self.system.used_memory()),
            memory_total_bytes: non_zero(self.system.total_memory()),
            uptime_seconds: Some(System::uptime()),
            filesystems,
            interfaces,
        }
    }
}

fn non_zero(value: u64) -> Option<u64> {
    (value > 0).then_some(value)
}
