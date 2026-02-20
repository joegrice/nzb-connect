import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { fetchVPNStatus, fetchVPNConfig, vpnConnect, vpnDisconnect } from '@/api'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { ShieldCheck, ShieldOff, Loader2 } from 'lucide-react'

function stateBadge(state: string) {
  switch (state) {
    case 'connected':    return <Badge variant="success">Connected</Badge>
    case 'connecting':   return <Badge variant="warning">Connecting</Badge>
    case 'disconnected': return <Badge variant="secondary">Disconnected</Badge>
    default:             return <Badge variant="outline">{state}</Badge>
  }
}

function formatUptime(seconds?: number): string {
  if (!seconds) return ''
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  const s = seconds % 60
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m ${s}s`
  return `${s}s`
}

export function VpnPanel() {
  const qc = useQueryClient()

  const { data: status, isLoading } = useQuery({
    queryKey: ['vpn-status'],
    queryFn: fetchVPNStatus,
    refetchInterval: 5000,
  })

  const { data: config } = useQuery({
    queryKey: ['vpn-config'],
    queryFn: fetchVPNConfig,
  })

  const connect = useMutation({
    mutationFn: vpnConnect,
    onSettled: () => qc.invalidateQueries({ queryKey: ['vpn-status'] }),
  })
  const disconnect = useMutation({
    mutationFn: vpnDisconnect,
    onSettled: () => qc.invalidateQueries({ queryKey: ['vpn-status'] }),
  })

  const isConnected = status?.state === 'connected'
  const isConnecting = status?.state === 'connecting'
  const isManaged = status?.managed ?? false

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2">
          {isConnected ? (
            <ShieldCheck className="h-5 w-5 text-green-500" />
          ) : (
            <ShieldOff className="h-5 w-5 text-muted-foreground" />
          )}
          VPN
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        {isLoading ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : (
          <>
            <div className="flex items-center justify-between">
              <div className="space-y-1">
                <div className="flex items-center gap-2">
                  {stateBadge(status?.state ?? 'disconnected')}
                  {isManaged && <Badge variant="outline" className="text-xs">Managed</Badge>}
                </div>
                {status?.interface_name && (
                  <p className="text-sm text-muted-foreground">Interface: {status.interface_name}</p>
                )}
                {status?.uptime_seconds !== undefined && status.uptime_seconds > 0 && (
                  <p className="text-sm text-muted-foreground">Uptime: {formatUptime(status.uptime_seconds)}</p>
                )}
                {status?.error && (
                  <p className="text-sm text-destructive">{status.error}</p>
                )}
              </div>

              {isManaged && (
                <div className="flex gap-2">
                  {isConnected || isConnecting ? (
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => disconnect.mutate()}
                      disabled={disconnect.isPending}
                    >
                      {disconnect.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : 'Disconnect'}
                    </Button>
                  ) : (
                    <Button
                      size="sm"
                      onClick={() => connect.mutate()}
                      disabled={connect.isPending}
                    >
                      {connect.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : 'Connect'}
                    </Button>
                  )}
                </div>
              )}
            </div>

            {config && (
              <div className="rounded-md bg-muted p-3 space-y-1">
                <p className="text-xs text-muted-foreground font-medium uppercase tracking-wide">Configuration</p>
                <div className="grid grid-cols-2 gap-1 text-sm">
                  <span className="text-muted-foreground">Mode</span>
                  <span>{config.protocol ? config.protocol : 'Bind-only'}</span>
                  {config.interface && (
                    <>
                      <span className="text-muted-foreground">Interface</span>
                      <span>{config.interface}</span>
                    </>
                  )}
                </div>
              </div>
            )}

            {!isManaged && (
              <p className="text-sm text-muted-foreground">
                Bind-only mode — manage your VPN connection externally.
                The app will bind NNTP connections to the configured interface.
              </p>
            )}
          </>
        )}
      </CardContent>
    </Card>
  )
}
