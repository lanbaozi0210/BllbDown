import van from 'vanjs-core'
import type { WorkRoute } from '..'
import type { RecentVideo } from '../history'
import { writeRecentHistory } from '../history'

const { button, div, img, span } = van.tags

const durationLabel = (seconds: number) => {
    if (!seconds) return '时长未知'
    const hours = Math.floor(seconds / 3600)
    const minutes = Math.floor(seconds % 3600 / 60)
    const rest = Math.floor(seconds % 60)
    return hours
        ? `${hours}:${String(minutes).padStart(2, '0')}:${String(rest).padStart(2, '0')}`
        : `${minutes}:${String(rest).padStart(2, '0')}`
}

const videoCard = (workRoute: WorkRoute, item: RecentVideo) => button({
    class: 'recent-video-card',
    type: 'button',
    title: `重新解析：${item.title}`,
    disabled: workRoute.btnLoading,
    onclick: () => workRoute.openRecent(item),
},
    div({ class: 'recent-video-cover' },
        img({ src: item.cover, alt: '', loading: 'lazy', referrerPolicy: 'no-referrer' }),
        span({ class: 'recent-video-duration' }, durationLabel(item.duration)),
    ),
    span({ class: 'recent-video-title' }, item.title),
)

export default (workRoute: WorkRoute) => div({
    class: 'recent-videos',
    hidden: () => workRoute.videoInfoCardMode.val !== 'hide' || workRoute.btnLoading.val,
},
    div({ class: 'recent-videos-heading' },
        div({},
            div({ class: 'welcome-eyebrow' }, '02 / RECENT'),
            div({ class: 'recent-videos-label' }, '最近解析'),
        ),
        button({
            class: 'recent-videos-clear', type: 'button',
            hidden: () => workRoute.recentVideos.val.length === 0,
            onclick: () => {
                workRoute.recentVideos.val = []
                writeRecentHistory([])
            },
        }, '清空记录'),
    ),
    () => workRoute.recentVideos.val.length
        ? div({ class: 'recent-videos-grid' }, workRoute.recentVideos.val.map(item => videoCard(workRoute, item)))
        : div({ class: 'recent-videos-empty' }, '解析过的视频会显示在这里，下次点开即可继续操作。'),
)
