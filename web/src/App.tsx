import { useState, useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { fetchStatus, fetchVPNStatus } from '@/api'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Queue } from '@/components/Queue'
import { History } from '@/components/History'
import { Servers } from '@/components/Servers'
import { AddNzb } from '@/components/AddNzb'
import { VpnPanel } from '@/components/VpnPanel'
import { Download, Clock, Settings, Sun, Moon } from 'lucide-react'

function formatSpeed(kbps: string): string {
  const n = Number(kbps)
  if (n <= 0) return ''
  if (n < 1024) return `${n.toFixed(0)} KB/s`
  return `${(n / 1024).toFixed(1)} MB/s`
}

function useTheme() {
  // Read current state from the DOM — already set synchronously by the
  // inline <script> in index.html before React renders, so no flash.
  const [dark, setDark] = useState<boolean>(() =>
    document.documentElement.classList.contains('dark')
  )

  useEffect(() => {
    const root = document.documentElement
    if (dark) {
      root.classList.add('dark')
    } else {
      root.classList.remove('dark')
    }
    localStorage.setItem('theme', dark ? 'dark' : 'light')
  }, [dark])

  return { dark, toggle: () => setDark(d => !d) }
}

function Header({ dark, onToggleTheme }: { dark: boolean; onToggleTheme: () => void }) {
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
      <div className="container mx-auto px-4 h-13 flex items-center justify-between max-w-5xl">
        <div className="flex items-center gap-2">
          <Download className="h-4 w-4 text-primary" />
          <span className="font-semibold text-sm">NZB Connect</span>
        </div>
        <div className="flex items-center gap-3 text-sm">
          {paused && <Badge variant="warning">Paused</Badge>}
          {speed && <span className="text-muted-foreground tabular-nums">{speed}</span>}
          <div className="flex items-center gap-1.5">
            <div className={`h-2 w-2 rounded-full shrink-0 ${vpnUp ? 'bg-green-500' : 'bg-muted-foreground/40'}`} />
            <span className="text-muted-foreground text-xs">
              {vpn?.interface_name || (vpnUp ? 'VPN' : 'No VPN')}
            </span>
          </div>
          <Button
            variant="ghost"
            size="icon"
            className="h-8 w-8"
            onClick={onToggleTheme}
            title={dark ? 'Switch to light mode' : 'Switch to dark mode'}
          >
            {dark ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
          </Button>
        </div>
      </div>
    </header>
  )
}

export function App() {
  const { dark, toggle } = useTheme()

  return (
    <div className="min-h-screen bg-background text-foreground">
      <Header dark={dark} onToggleTheme={toggle} />
      <main className="container mx-auto px-4 py-5 max-w-5xl">
        <Tabs defaultValue="downloads">
          <TabsList className="mb-4">
            <TabsTrigger value="downloads" className="gap-1.5">
              <Download className="h-4 w-4" /> Downloads
            </TabsTrigger>
            <TabsTrigger value="history" className="gap-1.5">
              <Clock className="h-4 w-4" /> History
            </TabsTrigger>
            <TabsTrigger value="settings" className="gap-1.5">
              <Settings className="h-4 w-4" /> Settings
            </TabsTrigger>
          </TabsList>

          <TabsContent value="downloads">
            <AddNzb />
            <Queue />
          </TabsContent>

          <TabsContent value="history">
            <History />
          </TabsContent>

          <TabsContent value="settings">
            <div className="space-y-6">
              <Servers />
              <VpnPanel />
            </div>
          </TabsContent>
        </Tabs>
      </main>
    </div>
  )
}
