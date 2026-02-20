import { useState, useRef, type DragEvent, type ChangeEvent } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { addNZBFile, addNZBUrl } from '@/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Upload, Link, Loader2 } from 'lucide-react'

export function AddNzb() {
  const qc = useQueryClient()
  const fileRef = useRef<HTMLInputElement>(null)
  const [category, setCategory] = useState('')
  const [url, setUrl] = useState('')
  const [dragOver, setDragOver] = useState(false)
  const [message, setMessage] = useState<{ type: 'success' | 'error'; text: string } | null>(null)

  const notify = (type: 'success' | 'error', text: string) => {
    setMessage({ type, text })
    setTimeout(() => setMessage(null), 4000)
  }

  const uploadMutation = useMutation({
    mutationFn: ({ file, cat }: { file: File; cat: string }) => addNZBFile(file, cat),
    onSuccess: (data) => {
      if (data.status) {
        notify('success', `Added: ${data.nzo_ids?.[0] ?? '?'}`)
        qc.invalidateQueries({ queryKey: ['queue'] })
      } else {
        notify('error', 'Failed to add NZB')
      }
    },
    onError: (err: Error) => notify('error', err.message),
  })

  const urlMutation = useMutation({
    mutationFn: ({ nzbUrl, cat }: { nzbUrl: string; cat: string }) => addNZBUrl(nzbUrl, cat),
    onSuccess: (data) => {
      if (data.status) {
        notify('success', `Added: ${data.nzo_ids?.[0] ?? '?'}`)
        setUrl('')
        qc.invalidateQueries({ queryKey: ['queue'] })
      } else {
        notify('error', 'Failed to add NZB URL')
      }
    },
    onError: (err: Error) => notify('error', err.message),
  })

  function handleFiles(files: FileList | null) {
    if (!files || files.length === 0) return
    Array.from(files).forEach(file => uploadMutation.mutate({ file, cat: category }))
  }

  function handleDrop(e: DragEvent<HTMLDivElement>) {
    e.preventDefault()
    setDragOver(false)
    handleFiles(e.dataTransfer.files)
  }

  function handleFileChange(e: ChangeEvent<HTMLInputElement>) {
    handleFiles(e.target.files)
    e.target.value = ''
  }

  function handleUrlSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!url.trim()) return
    urlMutation.mutate({ nzbUrl: url.trim(), cat: category })
  }

  return (
    <div className="space-y-2 mb-4">
      {/* Drop zone + category row */}
      <div className="flex gap-2">
        <div
          className={`flex-1 flex items-center gap-3 border-2 border-dashed rounded-md px-4 py-2.5 cursor-pointer transition-colors ${
            dragOver
              ? 'border-primary bg-primary/10'
              : 'border-muted-foreground/25 hover:border-primary/50 hover:bg-muted/40'
          }`}
          onDragOver={e => { e.preventDefault(); setDragOver(true) }}
          onDragLeave={() => setDragOver(false)}
          onDrop={handleDrop}
          onClick={() => fileRef.current?.click()}
        >
          {uploadMutation.isPending
            ? <Loader2 className="h-4 w-4 text-muted-foreground shrink-0 animate-spin" />
            : <Upload className="h-4 w-4 text-muted-foreground shrink-0" />
          }
          <span className="text-sm text-muted-foreground select-none">
            {uploadMutation.isPending ? 'Uploading…' : 'Drop .nzb or click to browse'}
          </span>
          <input
            ref={fileRef}
            type="file"
            accept=".nzb"
            multiple
            className="hidden"
            onChange={handleFileChange}
          />
        </div>
        <Input
          placeholder="Category"
          value={category}
          onChange={e => setCategory(e.target.value)}
          className="w-28 shrink-0"
        />
      </div>

      {/* URL row */}
      <form onSubmit={handleUrlSubmit} className="flex gap-2">
        <Input
          placeholder="https://…/file.nzb"
          value={url}
          onChange={e => setUrl(e.target.value)}
          className="flex-1"
        />
        <Button type="submit" variant="secondary" disabled={urlMutation.isPending || !url.trim()}>
          {urlMutation.isPending
            ? <Loader2 className="h-4 w-4 animate-spin" />
            : <Link className="h-4 w-4" />
          }
          <span>Add URL</span>
        </Button>
      </form>

      {/* Feedback */}
      {message && (
        <p className={`text-xs px-1 ${message.type === 'success' ? 'text-green-500' : 'text-destructive'}`}>
          {message.text}
        </p>
      )}
    </div>
  )
}
