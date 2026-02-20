import { useState, useEffect } from 'react'
import { useQuery } from '@tanstack/react-query'
import { fetchHistory, type HistorySlot } from '@/api'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { ChevronLeft, ChevronRight } from 'lucide-react'

const PAGE_SIZE = 15

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`
}

function formatDuration(seconds: number): string {
  if (seconds < 60) return `${seconds}s`
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${seconds % 60}s`
  return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`
}

function formatDate(unix: number): string {
  if (!unix) return '—'
  return new Date(unix * 1000).toLocaleString()
}

function statusBadge(slot: HistorySlot) {
  switch (slot.status) {
    case 'Completed': return <Badge variant="success">Completed</Badge>
    case 'Failed':    return <Badge variant="destructive">Failed</Badge>
    default:          return <Badge variant="outline">{slot.status}</Badge>
  }
}

export function History() {
  const [page, setPage] = useState(0)

  const { data, isLoading } = useQuery({
    queryKey: ['history'],
    queryFn: fetchHistory,
    refetchInterval: 10000,
  })

  const allSlots = data?.history?.slots ?? []
  const totalPages = Math.max(1, Math.ceil(allSlots.length / PAGE_SIZE))

  // Clamp page when total changes (e.g. history cleared)
  useEffect(() => {
    if (page >= totalPages) setPage(Math.max(0, totalPages - 1))
  }, [totalPages, page])

  const slots = allSlots.slice(page * PAGE_SIZE, (page + 1) * PAGE_SIZE)
  const start = page * PAGE_SIZE + 1
  const end = Math.min((page + 1) * PAGE_SIZE, allSlots.length)

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center justify-between">
          <span>History</span>
          <span className="text-sm font-normal text-muted-foreground">
            {allSlots.length} item{allSlots.length !== 1 ? 's' : ''}
          </span>
        </CardTitle>
      </CardHeader>
      <CardContent className="p-0">
        {isLoading ? (
          <p className="text-sm text-muted-foreground py-4 text-center">Loading…</p>
        ) : allSlots.length === 0 ? (
          <p className="text-sm text-muted-foreground py-8 text-center">No completed downloads</p>
        ) : (
          <>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Size</TableHead>
                  <TableHead>Duration</TableHead>
                  <TableHead>Completed</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {slots.map(slot => (
                  <TableRow key={slot.nzo_id}>
                    <TableCell>
                      <div>
                        <p className="font-medium text-sm truncate max-w-xs" title={slot.name}>{slot.name}</p>
                        {slot.category && <p className="text-xs text-muted-foreground">{slot.category}</p>}
                        {slot.fail_message && <p className="text-xs text-destructive mt-0.5">{slot.fail_message}</p>}
                      </div>
                    </TableCell>
                    <TableCell>{statusBadge(slot)}</TableCell>
                    <TableCell className="text-sm text-muted-foreground whitespace-nowrap">{formatBytes(slot.bytes)}</TableCell>
                    <TableCell className="text-sm text-muted-foreground whitespace-nowrap">{formatDuration(slot.download_time)}</TableCell>
                    <TableCell className="text-sm text-muted-foreground whitespace-nowrap">{formatDate(slot.completed)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>

            {totalPages > 1 && (
              <div className="flex items-center justify-between px-4 py-3 border-t">
                <span className="text-xs text-muted-foreground">
                  {start}–{end} of {allSlots.length}
                </span>
                <div className="flex items-center gap-1">
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-8 w-8"
                    onClick={() => setPage(p => p - 1)}
                    disabled={page === 0}
                  >
                    <ChevronLeft className="h-4 w-4" />
                  </Button>
                  <span className="text-xs text-muted-foreground px-2 tabular-nums">
                    {page + 1} / {totalPages}
                  </span>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-8 w-8"
                    onClick={() => setPage(p => p + 1)}
                    disabled={page >= totalPages - 1}
                  >
                    <ChevronRight className="h-4 w-4" />
                  </Button>
                </div>
              </div>
            )}
          </>
        )}
      </CardContent>
    </Card>
  )
}
