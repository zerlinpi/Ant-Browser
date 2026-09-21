import { useCallback, useEffect, useState } from 'react'
import { Activity, ExternalLink, Gauge, HardDrive, Monitor, RefreshCw, Server } from 'lucide-react'
import { Link } from 'react-router-dom'
import { Badge, Button, Card, Loading, StatCard } from '../../shared/components'
import { GetDashboardStats, GetMemoryStats, GetRunningInstances } from '../../wailsjs/go/main/App'
import type { BrowserProfile } from '../browser/types'

interface DashboardStats {
  totalInstances: number
  runningInstances: number
  proxyCount: number
  coreCount: number
  memUsedMB: number
  appVersion: string
}

interface MemoryStats {
  allocMB: number
  limitMB: number
  gcPercent: number
}

interface DashboardData {
  stats: DashboardStats
  memory: MemoryStats
  running: BrowserProfile[]
}

type DashboardState =
  | { status: 'loading' }
  | { status: 'ready'; data: DashboardData; refreshedAt: Date }
  | { status: 'degraded'; message: string }

function hasWailsBindings(): boolean {
  const app = (globalThis as { go?: { main?: { App?: Record<string, unknown> } } }).go?.main?.App
  return !!app && typeof app.GetDashboardStats === 'function' && typeof app.GetMemoryStats === 'function' && typeof app.GetRunningInstances === 'function'
}

function numberValue(value: unknown): number {
  const parsed = typeof value === 'number' ? value : Number(value)
  return Number.isFinite(parsed) ? parsed : 0
}

function normalizeStats(raw: Record<string, any> | null | undefined): DashboardStats {
  return {
    totalInstances: numberValue(raw?.totalInstances),
    runningInstances: numberValue(raw?.runningInstances),
    proxyCount: numberValue(raw?.proxyCount),
    coreCount: numberValue(raw?.coreCount),
    memUsedMB: numberValue(raw?.memUsedMB),
    appVersion: String(raw?.appVersion || '').trim(),
  }
}

function normalizeMemory(raw: Record<string, any> | null | undefined): MemoryStats {
  return {
    allocMB: numberValue(raw?.alloc_mb),
    limitMB: numberValue(raw?.limit_mb),
    gcPercent: numberValue(raw?.gc_percent),
  }
}

function formatMemory(value: number): string {
  if (!value) return '0 MB'
  return `${value >= 100 ? Math.round(value) : value.toFixed(1)} MB`
}

function instanceStatus(profile: BrowserProfile): { label: string; variant: 'success' | 'info' | 'warning' } {
  if (!profile.running) return { label: '已停止', variant: 'warning' }
  if (!profile.debugReady) return { label: '运行中 · 待就绪', variant: 'info' }
  return { label: '运行中', variant: 'success' }
}

export function DashboardPage() {
  const [state, setState] = useState<DashboardState>({ status: 'loading' })

  const load = useCallback(async () => {
    if (!hasWailsBindings()) {
      setState({ status: 'degraded', message: '当前为浏览器预览模式，未连接本地运行时。启动桌面应用后可查看实时数据。' })
      return
    }

    setState((current) => current.status === 'ready' ? current : { status: 'loading' })
    try {
      const [rawStats, rawMemory, running] = await Promise.all([
        GetDashboardStats(),
        GetMemoryStats(),
        GetRunningInstances(),
      ])
      setState({
        status: 'ready',
        data: {
          stats: normalizeStats(rawStats),
          memory: normalizeMemory(rawMemory),
          running: Array.isArray(running) ? running : [],
        },
        refreshedAt: new Date(),
      })
    } catch {
      setState({ status: 'degraded', message: '本地运行时暂时不可用，请确认桌面应用已完成启动后重试。' })
    }
  }, [])

  useEffect(() => { void load() }, [load])

  if (state.status === 'loading') {
    return <div className="flex min-h-[280px] items-center justify-center"><Loading text="正在读取本地运行时状态…" /></div>
  }

  if (state.status === 'degraded') {
    return (
      <div className="mx-auto flex min-h-[420px] max-w-3xl items-center justify-center">
        <Card className="w-full" padding="lg">
          <div className="flex flex-col items-center gap-4 py-8 text-center">
            <div className="flex h-12 w-12 items-center justify-center rounded-full bg-[var(--color-bg-muted)] text-[var(--color-text-muted)]">
              <Server className="h-6 w-6" aria-hidden="true" />
            </div>
            <div>
              <h1 className="text-lg font-semibold text-[var(--color-text-primary)]">本地运行时未连接</h1>
              <p className="mt-2 max-w-md text-sm leading-6 text-[var(--color-text-secondary)]">{state.message}</p>
            </div>
            <Button variant="secondary" size="sm" onClick={() => void load()}>
              <RefreshCw className="h-4 w-4" aria-hidden="true" />
              重新连接
            </Button>
          </div>
        </Card>
      </div>
    )
  }

  const { stats, memory, running } = state.data
  const { refreshedAt } = state
  const memoryValue = memory.allocMB || stats.memUsedMB
  const memoryLimit = memory.limitMB > 0 ? `上限 ${formatMemory(memory.limitMB)}` : '当前进程占用'

  return (
    <div className="w-full space-y-6 animate-fade-in">
      <div className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <div className="flex flex-wrap items-center gap-2">
            <h1 className="text-xl font-semibold text-[var(--color-text-primary)]">运行概览</h1>
            <Badge variant="success" size="sm" dot>本地运行时已连接</Badge>
          </div>
          <p className="mt-1 text-sm text-[var(--color-text-muted)]">仅显示当前桌面应用的实例、资源与运行状态</p>
        </div>
        <div className="flex items-center gap-3">
          <span className="text-xs text-[var(--color-text-muted)]">更新于 {refreshedAt.toLocaleTimeString('zh-CN', { hour12: false })}</span>
          <Button variant="secondary" size="sm" onClick={() => void load()}>
            <RefreshCw className="h-4 w-4" aria-hidden="true" />
            刷新
          </Button>
        </div>
      </div>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard title="浏览器实例" value={stats.totalInstances} icon={<Monitor className="h-5 w-5" aria-hidden="true" />} />
        <StatCard title="正在运行" value={stats.runningInstances} icon={<Activity className="h-5 w-5" aria-hidden="true" />} />
        <StatCard title="代理节点" value={stats.proxyCount} icon={<Gauge className="h-5 w-5" aria-hidden="true" />} />
        <StatCard title="内核" value={stats.coreCount} icon={<HardDrive className="h-5 w-5" aria-hidden="true" />} />
      </div>

      <div className="grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,1.5fr)_minmax(280px,1fr)]">
        <Card
          title="运行中的实例"
          subtitle={running.length > 0 ? `${running.length} 个实例正在运行` : '当前没有运行中的实例'}
          actions={<Link className="inline-flex items-center gap-1 text-xs font-medium text-[var(--color-accent)] hover:underline" to="/browser/list">管理实例 <ExternalLink className="h-3.5 w-3.5" aria-hidden="true" /></Link>}
          padding="none"
        >
          {running.length > 0 ? (
            <div className="divide-y divide-[var(--color-border-muted)]">
              {running.map((profile) => {
                const status = instanceStatus(profile)
                return (
                  <Link key={profile.profileId} to={`/browser/detail/${encodeURIComponent(profile.profileId)}`} className="flex items-center justify-between gap-4 px-5 py-4 transition-colors hover:bg-[var(--color-bg-muted)]/60">
                    <div className="min-w-0">
                      <p className="truncate text-sm font-medium text-[var(--color-text-primary)]">{profile.profileName || profile.profileId}</p>
                      <p className="mt-1 truncate text-xs text-[var(--color-text-muted)]">{profile.profileId}{profile.debugPort ? ` · 调试端口 ${profile.debugPort}` : ''}</p>
                    </div>
                    <Badge variant={status.variant} size="sm" dot>{status.label}</Badge>
                  </Link>
                )
              })}
            </div>
          ) : (
            <div className="px-5 py-12 text-center text-sm text-[var(--color-text-muted)]">没有运行中的实例，前往实例列表启动浏览器。</div>
          )}
        </Card>

        <Card title="运行时资源" subtitle={stats.appVersion ? `应用版本 ${stats.appVersion}` : '当前桌面应用'} padding="lg">
          <div className="space-y-5">
            <div>
              <div className="flex items-center justify-between gap-3">
                <span className="text-sm text-[var(--color-text-secondary)]">内存占用</span>
                <span className="text-sm font-semibold tabular-nums text-[var(--color-text-primary)]">{formatMemory(memoryValue)}</span>
              </div>
              {memory.limitMB > 0 ? <div className="mt-2 h-2 overflow-hidden rounded-full bg-[var(--color-bg-muted)]" role="progressbar" aria-label="内存占用" aria-valuemin={0} aria-valuemax={memory.limitMB} aria-valuenow={Math.min(memoryValue, memory.limitMB)}><div className="h-full rounded-full bg-[var(--color-accent)]" style={{ width: `${Math.min(100, (memoryValue / memory.limitMB) * 100)}%` }} /></div> : null}
              <p className="mt-2 text-xs text-[var(--color-text-muted)]">{memoryLimit}</p>
            </div>
            {memory.gcPercent > 0 ? <div className="flex items-center justify-between gap-3 border-t border-[var(--color-border-muted)] pt-4 text-sm"><span className="text-[var(--color-text-secondary)]">GC 百分比</span><span className="font-medium tabular-nums text-[var(--color-text-primary)]">{memory.gcPercent}%</span></div> : null}
            <Link to="/browser/logs" className="inline-flex items-center gap-1 text-xs font-medium text-[var(--color-accent)] hover:underline">查看运行日志 <ExternalLink className="h-3.5 w-3.5" aria-hidden="true" /></Link>
          </div>
        </Card>
      </div>
    </div>
  )
}
