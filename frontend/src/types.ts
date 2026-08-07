export type Surface = "desktop" | "control" | "agent" | "setup";

export interface APIErrorResponse {
  error?: {
    code?: string;
    message?: string;
  };
}

export interface ModuleConfig {
  id: string;
  instance_id?: string;
  enabled: boolean;
  base_url?: string;
  work_dir?: string;
  read_only: boolean;
}

export interface ModuleStatus {
  id: string;
  state: string;
  version?: string;
  expected_version: string;
  detail?: string;
  checked_at: string;
}

export interface DeviceStatus {
  version: string;
  device_id: string;
  name: string;
  online: boolean;
  tailnet_ip?: string;
  connection: string;
  derp_region?: string;
  last_seen: string;
  config_revision: number;
  modules: ModuleStatus[];
}

export interface DeviceView {
  id: string;
  name: string;
  agent_url: string;
  created_at: string;
  config: {
    modules: ModuleConfig[];
    shared_directories: Array<{
      id: string;
      name: string;
      permissions: string[];
    }>;
  };
  status: DeviceStatus;
}

export interface FileItem {
  name: string;
  size: number;
  modified: string;
  type: string;
}

export interface FileResource extends FileItem {
  path: string;
  files: FileItem[];
  folders: FileItem[];
}

export interface MediaItem {
  Id: string;
  Name: string;
  Type: string;
  ProductionYear?: number;
  RunTimeTicks?: number;
  SeriesName?: string;
}
