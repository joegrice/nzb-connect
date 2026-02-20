import { useState, useRef, type DragEvent, type ChangeEvent } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { addNZBFile, addNZBUrl } from '@/api'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Upload, Link } from 'lucide-react'

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
        notify('success', `Added download (ID: ${data.nzo_ids?.[0] ?? '?'})`)
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
        notify('success', `Added download (ID: ${data.nzo_ids?.[0] ?? '?'})`)
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
    Array.from(files).forEach(file => {
      uploadMutation.mutate({ file, cat: category })
    })
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
    <div className="space-y-4">
      <Card>
        <CardHeader className="pb-2">
          <CardTitle>Add NZB</CardTitle>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="category">Category (optional)</Label>
            <Input
              id="category"
              placeholder="e.g. tv, movies"
              value={category}
              onChange={e => setCategory(e.target.value)}
            />
          </div>

          {/* Drop zone */}
          <div
            className={`border-2 border-dashed rounded-lg p-8 text-center cursor-pointer transition-colors ${
              dragOver ? 'border-primary bg-primary/5' : 'border-muted-foreground/25 hover:border-primary/50'
            }`}
            onDragOver={e => { e.preventDefault(); setDragOver(true) }}
            onDragLeave={() => setDragOver(false)}
            onDrop={handleDrop}
            onClick={() => fileRef.current?.click()}
          >
            <Upload className="mx-auto h-8 w-8 text-muted-foreground mb-2" />
            <p className="text-sm font-medium">Drop NZB files here or click to browse</p>
            <p className="text-xs text-muted-foreground mt-1">Supports .nzb files</p>
            <input
              ref={fileRef}
              type="file"
              accept=".nzb"
              multiple
              className="hidden"
              onChange={handleFileChange}
            />
          </div>

          {uploadMutation.isPending && (
            <p className="text-sm text-muted-foreground">Uploading…</p>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-base">Add from URL</CardTitle>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleUrlSubmit} className="flex gap-2">
            <Input
              placeholder="https://example.com/file.nzb"
              value={url}
              onChange={e => setUrl(e.target.value)}
              className="flex-1"
            />
            <Button type="submit" disabled={urlMutation.isPending || !url.trim()}>
              <Link className="h-4 w-4 mr-1" />
              Add
            </Button>
          </form>
        </CardContent>
      </Card>

      {message && (
        <div className={`rounded-md px-4 py-3 text-sm ${
          message.type === 'success'
            ? 'bg-green-50 text-green-800 border border-green-200'
            : 'bg-red-50 text-red-800 border border-red-200'
        }`}>
          {message.text}
        </div>
      )}
    </div>
  )
}
