import van from 'vanjs-core'
import { now } from 'vanjs-router'
import { GLOBAL_HAS_LOGIN } from './mixin'

const { a, div, img } = van.tags

export default () => {
    const classStr = (name: string) => van.derive(() => `text-nowrap nav-link ${now.val.split('/')[0] == name ? 'active' : ''}`)

    return div({ class: 'app-header' },
        a({ class: 'brand-lockup', href: '#/work', title: '返回首页', 'aria-label': 'BllbDown，返回首页' },
            img({ class: 'brand-mark', src: '/brand-mark.svg', alt: '' }),
            div({ class: 'brand-copy' },
                div({ class: 'brand-name' }, 'BllbDown'),
                div({ class: 'brand-subtitle' }, 'LOCAL MEDIA UTILITY')
            )
        ),
        div({ class: 'nav nav-underline flex-nowrap overflow-auto app-nav' },
            div({ class: 'nav-item', hidden: () => !GLOBAL_HAS_LOGIN.val },
                a({ class: classStr('work'), href: '#/work' }, '下载')
            ),
            div({ class: 'nav-item', hidden: () => !GLOBAL_HAS_LOGIN.val },
                a({ class: classStr('task'), href: '#/task' }, '任务')
            ),
            div({ class: 'nav-item', hidden: () => !GLOBAL_HAS_LOGIN.val },
                a({ class: classStr('setting'), href: '#/setting' }, '设置')
            ),
            div({ class: 'nav-item', hidden: GLOBAL_HAS_LOGIN },
                a({ class: classStr('login'), href: '#/login' }, '扫码登录')
            ),
        )
    )
}
