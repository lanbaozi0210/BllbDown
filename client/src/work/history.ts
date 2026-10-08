import { ResJSON } from '../mixin'
import { TaskInDB } from './type'

export type RecentVideo = {
    idType: 'bv' | 'ep' | 'ss'
    value: string
    title: string
    cover: string
    duration: number
}

const STORAGE_KEY = 'bllbdown.recent-videos.v1'
const MAX_RECENT = 8

export const hasRecentHistory = () => {
    try { return localStorage.getItem(STORAGE_KEY) !== null } catch { return true }
}

export const readRecentHistory = (): RecentVideo[] => {
    try {
        const parsed: unknown = JSON.parse(localStorage.getItem(STORAGE_KEY) || '[]')
        if (!Array.isArray(parsed)) return []
        return parsed.filter((item): item is RecentVideo =>
            item && ['bv', 'ep', 'ss'].includes(item.idType)
            && typeof item.value === 'string'
            && typeof item.title === 'string'
            && typeof item.cover === 'string'
            && typeof item.duration === 'number'
        ).slice(0, MAX_RECENT)
    } catch { return [] }
}

export const writeRecentHistory = (items: RecentVideo[]) => {
    try { localStorage.setItem(STORAGE_KEY, JSON.stringify(items.slice(0, MAX_RECENT))) } catch { }
}

export const addRecentVideo = (items: RecentVideo[], item: RecentVideo): RecentVideo[] => {
    const next = [item, ...items.filter(existing => existing.idType !== item.idType || existing.value !== item.value)]
        .slice(0, MAX_RECENT)
    writeRecentHistory(next)
    return next
}

// 首次升级时，从已有下载任务中补出可用的最近视频入口；之后只记录实际解析结果。
export const recentVideosFromTasks = async (): Promise<RecentVideo[]> => {
    const res = await fetch('/api/getTaskList?page=0&pageSize=100').then(res => res.json()) as ResJSON<TaskInDB[]>
    if (!res.success) throw new Error(res.message)
    const seen = new Set<string>()
    return res.data.flatMap(task => {
        if (!/^BV1[a-zA-Z0-9]+$/.test(task.bvid) || seen.has(task.bvid)) return []
        seen.add(task.bvid)
        return [{ idType: 'bv' as const, value: task.bvid, title: task.title,
            cover: task.cover, duration: task.duration }]
    }).slice(0, MAX_RECENT)
}
