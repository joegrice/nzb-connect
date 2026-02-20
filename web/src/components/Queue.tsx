import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { fetchQueue, cancelDownload, type DownloadSlot } from '@/api'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Progress } from '@/components/ui/progress'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { X } from 'lucide-react'

function statusBadge(slot: DownloadSlot) {
  const isExtracting = slot.status === 'Extracting' || (slot.status === 'Extracting' && Number(slot.extract_pct) > 0)
  if (isExtracting) return <Badge variant="purple">Extracting</Badge>
  switch (slot.status) {
    case 'Downloading': return <Badge variant="default">Downloading</Badge>
    case 'Queued':      return <Badge variant="secondary">Queued</Badge>
    default:            return <Badge variant="outline">{slot.status}</Badge>
  }
}

function QueueSlot({ slot, onCancel }: { slot: DownloadSlot; onCancel: (id: string) => void }) {
  const pct = Number(slot.percentage)
  const extractPct = Number(slot.extract_pct)
  const isExtracting = slot.status === 'Extracting'

  return (
    <div className="py-4 border-b last:border-0">
      <div className="flex items-start justify-between gap-2 mb-2">
        <div className="flex-1 min-w-0">
          <p className="font-medium text-sm truncate" title={slot.filename}>{slot.filename}</p>
          {slot.cat && <p className="text-xs text-muted-foreground">{slot.cat}</p>}
        </div>
        <div className="flex items-center gap-2 shrink-0">
          {statusBadge(slot)}
          <Button
            variant="ghost"
            size="icon"
            className="h-7 w-7 text-muted-foreground hover:text-destructive"
            onClick={() => onCancel(slot.nzo_id)}
            title="Cancel download"
          >
            <X className="h-4 w-4" />
          </Button>
        </div>
      </div>

      {isExtracting ? (
        <div className="space-y-1">
          <Progress
            value={extractPct}
            className="h-2"
            indicatorClassName="bg-purple-500"
          />
          <div className="flex justify-between text-xs text-muted-foreground">
            <span className="truncate max-w-[70%]" title={slot.extract_file}>
              {slot.extract_file ? `Extracting: ${slot.extract_file}` : 'Extracting…'}
            </span>
            <span>{extractPct}%</span>
          </div>
        </div>
      ) : (
        <div className="space-y-1">
          <Progress value={pct} className="h-2" />
          <div className="flex justify-between text-xs text-muted-foreground">
            <span>{slot.sizeleft} remaining of {slot.size}</span>
            <span>{pct}%</span>
          </div>
        </div>
      )}
    </div>
  )
}

export function Queue() {
  const qc = useQueryClient()
  const { data, isLoading } = useQuery({
    queryKey: ['queue'],
    queryFn: fetchQueue,
    refetchInterval: 2000,
  })

  const cancel = useMutation({
    mutationFn: cancelDownload,
    onSuccess: () => qc.invalidateQueries({ queryKey: ['queue'] }),
  })

  const slots = data?.queue?.slots ?? []
  const isPaused = data?.queue?.paused ?? false

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center justify-between">
          <span>Queue</span>
          <div className="flex items-center gap-2 text-sm font-normal">
            {isPaused && <Badge variant="warning">Paused</Badge>}
            <span className="text-muted-foreground">{slots.length} item{slots.length !== 1 ? 's' : ''}</span>
          </div>
        </CardTitle>
      </CardHeader>
      <CardContent>
        {isLoading ? (
          <p className="text-sm text-muted-foreground py-4 text-center">Loading…</p>
        ) : slots.length === 0 ? (
          <p className="text-sm text-muted-foreground py-8 text-center">No active downloads</p>
        ) : (
          slots.map(slot => (
            <QueueSlot
              key={slot.nzo_id}
              slot={slot}
              onCancel={id => cancel.mutate(id)}
            />
          ))
        )}
      </CardContent>
    </Card>
  )
}
