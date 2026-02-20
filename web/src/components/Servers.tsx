import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { fetchServers, addServer, updateServer, deleteServer, testServer, type Server } from '@/api'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Badge } from '@/components/ui/badge'
import {
  Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, DialogTrigger,
} from '@/components/ui/dialog'
import { Plus, Pencil, Trash2, Wifi } from 'lucide-react'

type FormState = Omit<Server, 'id'> & { id?: string }

const emptyForm = (): FormState => ({
  name: '', host: '', port: 563, ssl: true, username: '', password: '',
  connections: 20, enabled: true,
})

function ServerForm({
  initial,
  onSave,
  onClose,
}: {
  initial: FormState
  onSave: (s: FormState) => void
  onClose: () => void
}) {
  const [form, setForm] = useState<FormState>(initial)
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<{ ok: boolean; msg: string } | null>(null)

  function set<K extends keyof FormState>(k: K, v: FormState[K]) {
    setForm(prev => ({ ...prev, [k]: v }))
    setTestResult(null)
  }

  async function handleTest() {
    setTesting(true)
    setTestResult(null)
    try {
      const r = await testServer(form)
      setTestResult({ ok: r.status, msg: r.message ?? r.error ?? '' })
    } catch (e) {
      setTestResult({ ok: false, msg: String(e) })
    } finally {
      setTesting(false)
    }
  }

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-3">
        <div className="col-span-2 space-y-1.5">
          <Label htmlFor="srv-name">Name</Label>
          <Input id="srv-name" value={form.name} onChange={e => set('name', e.target.value)} placeholder="My Server" />
        </div>
        <div className="col-span-2 space-y-1.5">
          <Label htmlFor="srv-host">Host</Label>
          <Input id="srv-host" value={form.host} onChange={e => set('host', e.target.value)} placeholder="news.example.com" />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="srv-port">Port</Label>
          <Input id="srv-port" type="number" value={form.port} onChange={e => set('port', Number(e.target.value))} />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="srv-conns">Connections</Label>
          <Input id="srv-conns" type="number" min={1} max={50} value={form.connections} onChange={e => set('connections', Number(e.target.value))} />
        </div>
        <div className="col-span-2 space-y-1.5">
          <Label htmlFor="srv-user">Username</Label>
          <Input id="srv-user" value={form.username} onChange={e => set('username', e.target.value)} />
        </div>
        <div className="col-span-2 space-y-1.5">
          <Label htmlFor="srv-pass">Password</Label>
          <Input id="srv-pass" type="password" value={form.password} onChange={e => set('password', e.target.value)} placeholder={form.id ? '(unchanged)' : ''} />
        </div>
        <div className="flex items-center gap-2">
          <Switch id="srv-ssl" checked={form.ssl} onCheckedChange={v => set('ssl', v)} />
          <Label htmlFor="srv-ssl">SSL</Label>
        </div>
        <div className="flex items-center gap-2">
          <Switch id="srv-enabled" checked={form.enabled} onCheckedChange={v => set('enabled', v)} />
          <Label htmlFor="srv-enabled">Enabled</Label>
        </div>
      </div>

      {testResult && (
        <p className={`text-sm ${testResult.ok ? 'text-green-600' : 'text-destructive'}`}>
          {testResult.ok ? '✓ Connected successfully' : `✗ ${testResult.msg}`}
        </p>
      )}

      <DialogFooter className="gap-2">
        <Button variant="outline" onClick={handleTest} disabled={testing || !form.host}>
          <Wifi className="h-4 w-4 mr-1" />
          {testing ? 'Testing…' : 'Test'}
        </Button>
        <Button variant="outline" onClick={onClose}>Cancel</Button>
        <Button onClick={() => onSave(form)} disabled={!form.host}>Save</Button>
      </DialogFooter>
    </div>
  )
}

export function Servers() {
  const qc = useQueryClient()
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<FormState | null>(null)

  const { data, isLoading } = useQuery({ queryKey: ['servers'], queryFn: fetchServers })

  const add = useMutation({
    mutationFn: addServer,
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['servers'] }); setOpen(false) },
  })
  const update = useMutation({
    mutationFn: ({ id, ...rest }: FormState & { id: string }) => updateServer(id, rest),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ['servers'] }); setEditing(null) },
  })
  const remove = useMutation({
    mutationFn: deleteServer,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['servers'] }),
  })

  function handleSave(form: FormState) {
    if (form.id) {
      update.mutate(form as FormState & { id: string })
    } else {
      add.mutate(form)
    }
  }

  const servers = data?.servers ?? []

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center justify-between">
          <span>NNTP Servers</span>
          <Dialog open={open} onOpenChange={setOpen}>
            <DialogTrigger asChild>
              <Button size="sm" onClick={() => setOpen(true)}>
                <Plus className="h-4 w-4 mr-1" /> Add Server
              </Button>
            </DialogTrigger>
            <DialogContent>
              <DialogHeader>
                <DialogTitle>Add Server</DialogTitle>
              </DialogHeader>
              <ServerForm initial={emptyForm()} onSave={handleSave} onClose={() => setOpen(false)} />
            </DialogContent>
          </Dialog>
        </CardTitle>
      </CardHeader>
      <CardContent>
        {isLoading ? (
          <p className="text-sm text-muted-foreground text-center py-4">Loading…</p>
        ) : servers.length === 0 ? (
          <p className="text-sm text-muted-foreground text-center py-8">No servers configured</p>
        ) : (
          <div className="space-y-2">
            {servers.map(srv => (
              <div key={srv.id} className="flex items-center justify-between rounded-lg border p-3">
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <p className="font-medium text-sm">{srv.name || srv.host}</p>
                    {srv.ssl && <Badge variant="outline" className="text-xs">SSL</Badge>}
                    {!srv.enabled && <Badge variant="secondary" className="text-xs">Disabled</Badge>}
                  </div>
                  <p className="text-xs text-muted-foreground">
                    {srv.host}:{srv.port} · {srv.connections} conn{srv.connections !== 1 ? 's' : ''}
                    {srv.username ? ` · ${srv.username}` : ''}
                  </p>
                </div>
                <div className="flex items-center gap-1 ml-2">
                  <Dialog open={editing?.id === srv.id} onOpenChange={o => !o && setEditing(null)}>
                    <DialogTrigger asChild>
                      <Button variant="ghost" size="icon" className="h-8 w-8" onClick={() => setEditing({ ...srv })}>
                        <Pencil className="h-4 w-4" />
                      </Button>
                    </DialogTrigger>
                    <DialogContent>
                      <DialogHeader>
                        <DialogTitle>Edit Server</DialogTitle>
                      </DialogHeader>
                      {editing && (
                        <ServerForm
                          initial={editing}
                          onSave={handleSave}
                          onClose={() => setEditing(null)}
                        />
                      )}
                    </DialogContent>
                  </Dialog>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-8 w-8 text-muted-foreground hover:text-destructive"
                    onClick={() => remove.mutate(srv.id)}
                  >
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </div>
              </div>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  )
}
