import van, { State } from 'vanjs-core'
import { VanComponent, formatSeconds } from '../../mixin'
import { PageInParseResult, PlayInfo, VideoFormat } from '../type'
import { WorkRoute } from '..'
import { createTask, getPlayInfo, getYTDLPInfo } from '../data'
import PQueue from 'p-queue'

const { a, button, div, input, select, option, label } = van.tags

type Option = {
    workRoute: WorkRoute
}

const videoFormatMap: Record<VideoFormat, string> = {
    127: "超高清 8K",
    126: "杜比视界",
    125: "真彩 HDR",
    120: "超清 4K",
    116: "高清 1080P60",
    112: "高清 1080P+",
    80: "高清 1080P",
    74: "高清 720P60",
    64: "高清 720P",
    32: "清晰 480P",
    16: "流畅 360P",
    6: "极速 240P",
}

const codecMap: Record<12 | 7 | 13, string> = {
    12: "HEVC (hev1)",
    7: "AVC (avc1)",
    13: "AV1 (av01)",
}

type QualityPreset = 'best' | '1080' | '720'

const qualityIndex = (formats: VideoFormat[], preset: QualityPreset): number => {
    if (preset === 'best' || formats.length === 0) return 0
    const preferred = preset === '1080' ? [80, 116, 112] : [64, 74]
    for (const format of preferred) {
        const index = formats.indexOf(format as VideoFormat)
        if (index >= 0) return index
    }
    const limit = preset === '1080' ? 80 : 64
    const lowerIndex = formats.findIndex(format => format < limit)
    return lowerIndex >= 0 ? lowerIndex : formats.length - 1
}

export class ParseModalComp implements VanComponent {
    element: HTMLElement

    totalCount: State<number>
    finishCount = van.state(0)

    abortControllers: AbortController[] = []

    currentController?: AbortController

    allPlayInfo: State<{
        page: PageInParseResult
        info: PlayInfo | null
        selected: State<boolean>
        formatIndex: State<number>
    }[]> = van.state([])

    /** 该属性用于在点击“开始下载”按钮后使按钮变为禁用状态，防止多次点击 */
    downloadBtnDisabled = van.state(false)

    /** 下载类型：audio 保留原音频，audio_mp3 转为 MP3 */
    downloadType = van.state<'audio' | 'audio_mp3' | 'video' | 'merge'>('merge')

    /** 批量画质预设；单条视频仍可在列表中单独调整 */
    qualityPreset = van.state<QualityPreset>('best')

    /** 优先视频编码格式：12 hev1, 7 avc1, 13 av01 */
    preferredCodec = van.state<12 | 7 | 13>(12)
    /** 是否优先使用HiRes（flac）音频 */
    preferHiResAudio = van.state(true)

    errorList: State<string[]> = van.state([])

    constructor(public option: Option) {
        this.totalCount = van.derive(() => option.workRoute.selectedPages.val.length)
        const allFinish = van.derive(() => this.totalCount.val == this.finishCount.val)
        this.element = div({ class: `modal fade`, tabIndex: -1 },
            div({ class: () => `modal-dialog modal-xl modal-fullscreen-xl-down ${(this.totalCount.val + this.errorList.val.length) < 10 ? '' : 'modal-dialog-scrollable'}` },
                div({ class: `modal-content` },
                    div({ class: `modal-header` },
                        div({ class: `h5 modal-title` }, () => allFinish.val ? '批量下载' : '批量解析'),
                        button({ class: `btn-close`, 'data-bs-dismiss': `modal` })
                    ),
                    div({ class: `modal-body vstack gap-3`, tabIndex: -1, style: 'outline: none;' },
                        this.ParseProgress(),
                        div({ class: 'vstack gap-2', hidden: () => this.errorList.val.length == 0 || !allFinish.val },
                            div({ class: 'text-danger' }, () => `以下 ${this.errorList.val.length} 个视频解析失败`),
                            () => div({ class: 'list-group' },
                                this.errorList.val.map(error => div({ class: 'list-group-item disabled' }, error))
                            )
                        ),
                        this.ListGroup()
                    ),
                    this.ModalFooter()
                )
            )
        )

        this.element.addEventListener('hidden.bs.modal', () => {
            this.allPlayInfo.val = []
            this.finishCount.val = this.totalCount.val
            for (const controller of this.abortControllers) {
                controller.abort()
            }
            this.abortControllers = []
        })

        this.element.addEventListener('show.bs.modal', () => {
            this.start()
        })
    }

    /** 开始解析 */
    async start() {
        this.finishCount.val = 0
        this.errorList.val = []
        const queue = new PQueue({ concurrency: 10 })
        for (const page of this.option.workRoute.selectedPages.val) {
            queue.add(async () => {
                if (this.totalCount.val == this.finishCount.val) return
                const controller = new AbortController()
                this.abortControllers.push(controller)
                let playInfo: PlayInfo
                if (page.sourceURL) {
                    const yt = await getYTDLPInfo(page.sourceURL)
                    const media = (url: string, video: boolean): any => ({ id: 80, baseUrl: url, backupUrl: [], bandwidth: 0, mimeType: video ? 'video/mp4' : 'audio/mp4', codecs: video ? 'avc1' : 'mp4a', width: video ? yt.width : 0, height: video ? yt.height : 0, frameRate: '', codecid: 7 })
                    playInfo = { accept_quality: [80], dash: { duration: yt.duration, video: yt.video ? [media(yt.video, true)] : [], audio: yt.audio ? [media(yt.audio, false)] : [], flac: null } }
                } else playInfo = await getPlayInfo(page.bvid, page.cid, controller)
                playInfo.accept_quality = [...new Set(playInfo.dash.video.map(video => video.id))].sort((a, b) => b - a)
                this.allPlayInfo.val = this.allPlayInfo.val.concat({
                    page,
                    info: playInfo,
                    selected: van.state(true),
                    formatIndex: van.state(qualityIndex(playInfo.accept_quality, this.qualityPreset.val)),
                })
                this.finishCount.val++
            }).catch(() => {
                this.finishCount.val++
                const badgeNotNum = !page.badge.match(/^\d+$/)
                this.errorList.val = this.errorList.val.concat(`${page.part}${badgeNotNum ? ` - ${page.badge}` : ''}`)
            })
        }
        await queue.onIdle()
    }

    selectQuality(preset: QualityPreset) {
        this.qualityPreset.val = preset
        this.allPlayInfo.val.forEach(item => {
            item.formatIndex.val = qualityIndex(item.info!.accept_quality, preset)
        })
    }

    download() {
        const selectedPlayInfos = this.allPlayInfo.val.filter(info => info.selected.val)
        const workRoute = this.option.workRoute
        const isAudioDownload = this.downloadType.val === 'audio' || this.downloadType.val === 'audio_mp3'
        this.downloadBtnDisabled.val = true
        // 需要传递给服务器，需要创建下载任务的数据列表
        createTask(selectedPlayInfos.map(info => {
            const badgeNotNum = !info.page.badge.match(/^\d+$/)
            const isVideoMode = workRoute.videoInfoCardMode.val == 'video'
            const cardTitle = workRoute.videoInfoCardData.val.title
            const owner = workRoute.videoInfoCardData.val.staff.length > 0
                ? workRoute.videoInfoCardData.val.staff[0].split("[")[0].trim()
                : workRoute.videoInfoCardData.val.owner.name.trim()
            const activeVideoInfo = getActiveFormatVideo(info.info!, info.info!.accept_quality[info.formatIndex.val], this.preferredCodec.val)
            const pagesLength = workRoute.videoInfoCardData.val.pages.length

            return ({
                bvid: info.page.bvid,
                cid: info.page.cid,
                cover: workRoute.videoInfoCardData.val.cover,
                title: (badgeNotNum
                    ? [
                        info.page.part.trim(),
                        `[${info.page.badge.trim()}]`,
                        `[${cardTitle.trim()}]`,
                        isAudioDownload ? '' : `[${videoFormatMap[info.info!.accept_quality[info.formatIndex.val]]}]`,
                        `[${formatSeconds(info.info!.dash.duration)}]`
                    ]
                    : [
                        pagesLength == 1 ? workRoute.allSection.val[workRoute.sectionTabsActiveIndex.val].title : `[${cardTitle.trim()}]`,
                        workRoute.sectionPages.val.length == 1 ? '' : `[${info.page.badge.trim()}]`,
                        info.page.part.trim(),
                        isVideoMode ? `[${owner}]` : '',
                        isAudioDownload ? '' : `[${videoFormatMap[info.info!.accept_quality[info.formatIndex.val]]}]`,
                        `[${formatSeconds(info.info!.dash.duration)}]`
                    ]).filter(p => p).join(' '),
                format: info.info!.accept_quality[info.formatIndex.val],
                owner,
                audio: getAudioURL(info.info!, this.downloadType.val !== 'audio' && this.preferHiResAudio.val),
                duration: info.info!.dash.duration,
                downloadType: this.downloadType.val,
                ...activeVideoInfo
            })
        })).then(() => {
            workRoute.parseModal.hide()
        }).catch(error => {
            alert(error.message)
        }).finally(() => {
            this.downloadBtnDisabled.val = false
        })
    }

    ParseProgress() {
        return div({ class: 'vstack gap-3', hidden: () => this.totalCount.val == this.finishCount.val },
            div({ class: 'text-center fs-5' }, () => `正在解析，剩余 ${this.totalCount.val - this.finishCount.val} 项`),
            div({ class: 'progress' }, div({
                class: 'progress-bar progress-bar-striped progress-bar-animated',
                style: () => `width: ${this.finishCount.val / this.totalCount.val * 100}%`
            },)),
        )
    }

    ListGroup() {
        return () => div({ class: 'list-group', hidden: () => this.totalCount.val != this.finishCount.val },
            this.allPlayInfo.val.filter(info => info.info)
                .sort((a, b) => a.page.page - b.page.page)
                .map(info => {
                    const badgeNotNum = !info.page.badge.match(/^\d+$/)

                    return div({
                        class: () => `list-group-item user-select-none py-0 ${info.info ? '' : 'disabled'}`,
                        role: 'button',
                        onclick(event) {
                            if ((event.target as HTMLElement).getAttribute('class')?.match(/dropdown-?/)) return
                            info.selected.val = !info.selected.val
                        }
                    },
                        div({ class: 'hstack gap-2' },
                            div({ class: 'hstack gap-3 flex-fill py-1' },
                                input({
                                    class: 'form-check-input', type: 'checkbox', checked: info.selected,
                                }),
                                div({},
                                    div((badgeNotNum ? '' : `${info.page.badge}. `) + info.page.part),
                                    badgeNotNum ? div({ class: info.page.part ? 'small text-secondary' : '' },
                                        info.page.badge) : ''
                                ),
                            ),
                            div({ class: 'dropdown', hidden: () => _that.downloadType.val === 'audio' || _that.downloadType.val === 'audio_mp3' },
                                div({ class: 'dropdown-toggle py-2 text-primary', 'data-bs-toggle': 'dropdown' },
                                    () => videoFormatMap[info.info!.accept_quality[info.formatIndex.val]]
                                ),
                                () => {
                                    return div({ class: 'dropdown-menu shadow' },
                                        info.info!.accept_quality.map((formatID, index) => {
                                            return div({
                                                class: () => `dropdown-item ${info.formatIndex.val == index ? 'active' : ''}`,
                                                onclick() {
                                                    info.formatIndex.val = index
                                                }
                                            }, videoFormatMap[formatID as VideoFormat])
                                        })
                                    )
                                }
                            )
                        )
                    )
                })
        )
    }

    ModalFooter() {
        const _that = this

        const selectedCount = van.derive(() => this.allPlayInfo.val.filter(info => info.selected.val).length)
        const totalCount = van.derive(() => this.allPlayInfo.val.length)
        /** 解析完成列表全部选中 */
        const allSelected = van.derive(() => selectedCount.val == totalCount.val)
        /** 是否全部解析完成 */
        const allFinish = van.derive(() => this.totalCount.val == this.finishCount.val)

        return div({ class: `modal-footer` },
            div({ class: 'me-auto', hidden: () => !allFinish.val || totalCount.val == 0 },
                div({ class: 'hstack gap-3 text-nowrap' },
                    () => `已选择 (${selectedCount.val}/${totalCount.val}) 项`,
                    select({
                        class: 'form-select form-select-sm',
                        value: _that.downloadType,
                        oninput: (e) => _that.downloadType.val = (e.target as HTMLSelectElement).value as 'audio' | 'audio_mp3' | 'video' | 'merge'
                    },
                        option({ value: 'merge' }, '音视频合并'),
                        option({ value: 'audio' }, '仅音频（M4A）'),
                        option({ value: 'audio_mp3' }, '仅音频（MP3）'),
                        option({ value: 'video' }, '仅视频')
                    ),
                    div({ hidden: () => _that.downloadType.val === 'audio' || _that.downloadType.val === 'audio_mp3' },
                        select({
                            class: 'form-select form-select-sm',
                            'aria-label': '批量选择视频画质',
                            value: _that.qualityPreset,
                            oninput: (e) => _that.selectQuality((e.target as HTMLSelectElement).value as QualityPreset)
                        },
                            option({ value: 'best' }, '画质：最高可用'),
                            option({ value: '1080' }, '画质：1080P'),
                            option({ value: '720' }, '画质：720P')
                        )
                    ),
                    div({ hidden: () => _that.downloadType.val === 'audio' || _that.downloadType.val === 'audio_mp3' },
                        select({
                            class: 'form-select form-select-sm',
                            'aria-label': '优先视频编码',
                            value: _that.preferredCodec,
                            oninput: (e) => _that.preferredCodec.val = Number((e.target as HTMLSelectElement).value) as 12 | 7 | 13
                        },
                            option({ value: '12' }, 'HEVC (hev1)'),
                            option({ value: '7' }, 'AVC (avc1)'),
                            option({ value: '13' }, 'AV1 (av01)')
                        )
                    ),
                    div({ class: 'form-check form-check-inline', hidden: () => _that.downloadType.val === 'audio' || _that.downloadType.val === 'video' },
                        input({
                            type: 'checkbox',
                            class: 'form-check-input',
                            id: 'preferHiResAudio',
                            checked: _that.preferHiResAudio,
                            oninput: (e) => _that.preferHiResAudio.val = (e.target as HTMLInputElement).checked
                        }),
                        label({ class: 'form-check-label', for: 'preferHiResAudio' },
                            () => _that.downloadType.val === 'audio_mp3' ? '优先无损音源（输出仍为 MP3）' : 'Hi-Res')
                    )
                ),
                div({
                    class: 'quality-hint',
                    hidden: () => _that.qualityPreset.val === 'best' || _that.downloadType.val === 'audio' || _that.downloadType.val === 'audio_mp3'
                }, '部分视频没有目标画质时，会选最接近的可用画质；也可在每条视频右侧单独调整。')
            ),
            button({
                class: `btn btn-secondary`,
                'data-bs-dismiss': `modal`,
                hidden: allFinish
            }, '取消解析'),
            button({
                class: 'btn btn-secondary', hidden: () => !allFinish.val || allSelected.val || totalCount.val == 0,
                onclick() {
                    _that.allPlayInfo.val.forEach(info => info.selected.val = true)
                }
            }, '全选'),
            button({
                class: 'btn btn-warning', hidden: () => !allFinish.val || !allSelected.val || totalCount.val == 0,
                onclick() {
                    _that.allPlayInfo.val.forEach(info => info.selected.val = false)
                }
            }, '全不选'),
            button({
                class: `btn btn-primary`, onclick() {
                    _that.download()
                },
                hidden: () => !allFinish.val,
                disabled: () => selectedCount.val <= 0 || _that.downloadBtnDisabled.val
            }, '开始下载'),
        )
    }
}

const getAudioURL = (playInfo: PlayInfo, preferHiRes: boolean = true): string => {
    if (preferHiRes && playInfo.dash.flac) {
        return playInfo.dash.flac.audio.baseUrl
    } else {
        return playInfo.dash.audio.sort((a, b) => b.id - a.id)[0].baseUrl
    }
}

const getActiveFormatVideo = (playInfo: PlayInfo, format: VideoFormat, preferredCodec: 12 | 7 | 13 = 12): { video: string, width: number, height: number } => {
    // 优先级顺序：用户首选编码格式，然后按默认优先级 12 > 7 > 13
    const codecOrder = [preferredCodec, 12, 7, 13].filter((value, index, self) => self.indexOf(value) === index)
    for (const code of codecOrder) {
        for (const item of playInfo.dash.video) {
            if (item.id == format && item.codecid == code) {
                return {
                    video: item.baseUrl,
                    width: item.width,
                    height: item.height
                }
            }
        }
    }
    throw new Error('未找到对应视频分辨率格式')
}
