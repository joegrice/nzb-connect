import { useQuery } from '@tanstack/react-query'
import { fetchStatus, fetchVPNStatus } from '@/api'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Badge } from '@/components/ui/badge'
import { Queue } from '@/components/Queue'
import { History } from '@/components/History'
import { Servers } from '@/components/Servers'
import { AddNzb } from '@/components/AddNzb'
import { VpnPanel } from '@/components/VpnPanel'
import { Download, Clock, Server, Plus, Shield } from 'lucide-react'

function formatSpeed(kbps: string): string {
  const n = Number(kbps)
  if (n <= 0) return '0 KB/s'
  if (n < 1024) return `${n.toFixed(0)} KB/s`
  return `${(n / 1024).toFixed(1)} MB/s`
}

function Header() {
  const { data: status } = useQuery({
    queryKey: ['status'],
    queryFn: fetchStatus,
    refetchInterval: 3000,
  })
  const { data: vpn } = useQuery({
    queryKey: ['vpn-status'],
    queryFn: fetchVPNStatus,
    refetchInterval: 5000,
  })

  const speed = formatSpeed(status?.status?.kbpersec ?? '0')
  const vpnUp = vpn?.state === 'connected'
  const paused = status?.status?.paused ?? false

  return (
    <header className="border-b bg-background sticky top-0 z-10">
      <div className="container mx-auto px-4 h-14 flex items-center justify-between max-w-5xl">
        <div className="flex items-center gap-2">
          <Download className="h-5 w-5 text-primary" />
          <span className="font-semibold text-base">NZB Connect</span>
        </div>
        <div className="flex items-center gap-3 text-sm">
          {paused && <Badge variant="warning">Paused</Badge>}
          <span className="text-muted-foreground">{speed}</span>
          <div className="flex items-center gap-1.5">
            <div className={`h-2 w-2 rounded-full ${vpnUp ? 'bg-green-500' : 'bg-muted-foreground'}`} />
            <span className="text-muted-foreground text-xs">
              {vpn?.interface_name ?? (vpnUp ? 'VPN' : 'No VPN')}
            </span>
          </div>
        </div>
      </div>
    </header>
  )
}

export function App() {
  return (
    <div className="min-h-screen bg-background text-foreground">
      <Header />
      <main className="container mx-auto px-4 py-6 max-w-5xl">
        <Tabs defaultValue="queue">
          <TabsList className="mb-4">
            <TabsTrigger value="queue" className="gap-1.5">
              <Download className="h-4 w-4" /> Queue
            </TabsTrigger>
            <TabsTrigger value="history" className="gap-1.5">
              <Clock className="h-4 w-4" /> History
            </TabsTrigger>
            <TabsTrigger value="add" className="gap-1.5">
              <Plus className="h-4 w-4" /> Add NZB
            </TabsTrigger>
            <TabsTrigger value="servers" className="gap-1.5">
              <Server className="h-4 w-4" /> Servers
            </TabsTrigger>
            <TabsTrigger value="vpn" className="gap-1.5">
              <Shield className="h-4 w-4" /> VPN
            </TabsTrigger>
          </TabsList>

          <TabsContent value="queue">
            <Queue />
          </TabsContent>
          <TabsContent value="history">
            <History />
          </TabsContent>
          <TabsContent value="add">
            <AddNzb />
          </TabsContent>
          <TabsContent value="servers">
            <Servers />
          </TabsContent>
          <TabsContent value="vpn">
            <VpnPanel />
          </TabsContent>
        </Tabs>
      </main>
    </div>
  )
}
