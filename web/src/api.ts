export type DownloadSlot = {
  nzo_id: string
  filename: string
  cat: string
  status: string
  mb: string
  mbleft: string
  percentage: string
  size: string
  sizeleft: string
  timeleft: string
  extract_pct: string
  extract_file: string
}

export type QueueResponse = {
  queue: {
    paused: boolean
    slots: DownloadSlot[]
    speed: string
    noofslots: number
  }
}

export type HistorySlot = {
  nzo_id: string
  name: string
  category: string
  status: string
  fail_message: string
  storage: string
  bytes: number
  download_time: number
  completed: number
}

export type HistoryResponse = {
  history: {
    slots: HistorySlot[]
    noofslots: number
  }
}

export type StatusResponse = {
  status: {
    paused: boolean
    speed: string
    kbpersec: string
    mbleft: string
    noofslots_total: number
    version: string
    vpn_connected: boolean
    vpn_interface: string
  }
}

export type Server = {
  id: string
  name: string
  host: string
  port: number
  ssl: boolean
  username: string
  password: string
  connections: number
  enabled: boolean
}

export type ServersResponse = {
  servers: Server[]
}

export type VPNStatus = {
  state: string
  interface_name: string
  error: string
  managed: boolean
  connected_at?: string
  uptime_seconds?: number
}

export type VPNConfig = {
  enabled: boolean
  protocol: string
  interface: string
  wireguard?: Record<string, unknown>
  openvpn?: Record<string, unknown>
}

async function apiFetch<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, init)
  if (!res.ok) throw new Error(`HTTP ${res.status}: ${res.statusText}`)
  return res.json() as Promise<T>
}

export async function fetchQueue(): Promise<QueueResponse> {
  return apiFetch<QueueResponse>('/api?mode=queue')
}

export async function fetchHistory(): Promise<HistoryResponse> {
  return apiFetch<HistoryResponse>('/api?mode=history')
}

export async function fetchStatus(): Promise<StatusResponse> {
  return apiFetch<StatusResponse>('/api?mode=status')
}

export async function fetchServers(): Promise<ServersResponse> {
  return apiFetch<ServersResponse>('/api/servers')
}

export async function fetchVPNStatus(): Promise<VPNStatus> {
  return apiFetch<VPNStatus>('/api/vpn/status')
}

export async function fetchVPNConfig(): Promise<VPNConfig> {
  return apiFetch<VPNConfig>('/api/vpn')
}

export async function addNZBFile(file: File, category: string): Promise<{ status: boolean; nzo_ids?: string[] }> {
  const form = new FormData()
  form.append('nzbfile', file)
  if (category) form.append('cat', category)
  return apiFetch('/api', { method: 'POST', body: form })
}

export async function addNZBUrl(url: string, category: string): Promise<{ status: boolean; nzo_ids?: string[] }> {
  const form = new FormData()
  form.append('name', url)
  if (category) form.append('cat', category)
  return apiFetch('/api', { method: 'POST', body: form })
}

export async function cancelDownload(id: string): Promise<void> {
  await apiFetch(`/api/queue/${id}`, { method: 'DELETE' })
}

export async function addServer(server: Partial<Server>): Promise<{ status: boolean; server?: Server }> {
  return apiFetch('/api/servers', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(server),
  })
}

export async function updateServer(id: string, server: Partial<Server>): Promise<{ status: boolean }> {
  return apiFetch(`/api/servers/${id}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(server),
  })
}

export async function deleteServer(id: string): Promise<{ status: boolean }> {
  return apiFetch(`/api/servers/${id}`, { method: 'DELETE' })
}

export async function testServer(server: Partial<Server>): Promise<{ status: boolean; message?: string; error?: string }> {
  return apiFetch('/api/servers/test', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(server),
  })
}

export async function vpnConnect(): Promise<{ status: boolean; error?: string }> {
  return apiFetch('/api/vpn/connect', { method: 'POST' })
}

export async function vpnDisconnect(): Promise<{ status: boolean; error?: string }> {
  return apiFetch('/api/vpn/disconnect', { method: 'POST' })
}

export async function updateVPNConfig(config: Partial<VPNConfig>): Promise<{ status: boolean }> {
  return apiFetch('/api/vpn', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(config),
  })
}
