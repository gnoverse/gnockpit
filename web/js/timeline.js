// The validators card's views, Live, Timeline and Ranking, and its
// Timeline view, which lists the validator set changes the block
// history holds, newest first, from /api/history?changes=; the pages
// it shows are read again while shown.

/** @import { HistoryChange, HistoryChangeMember, HistoryFilled, HistoryReport } from '../types/history.js' */

import { $, $in } from './dom.js'
import { thousands, timeAgo, utcMinute } from './format.js'
import { shortAddr, valLink } from './names.js'
import { present } from './present.js'
import { rankingHidden, rankingShown } from './ranking.js'
import { boundedRead, errorOf, failureMessage } from './read.js'
import { storageGet, storageSet } from './storage.js'

// offer is what offerRanking does: start sets it, as the card's view
// lives in start. Before start, or after a start that failed, an
// answer changes nothing.
/** @type {(on: boolean) => void} */
let offer = function () {}

// offerRanking takes the server's word on the ranking, as each
// /api/boot answer gives it. While it is on, the card offers the
// Ranking view, and shows it if it waits for it: asked by ?view= or
// the saved view, or left for Live by an answer saying off. While it
// is off, the card shows Live in its stead.
/** @param {boolean} on */
export function offerRanking(on) {
    offer(on)
}

/**
 * A segment of a change's line: its text, whether it is bold, and the
 * address of the validator whose page its link opens, if any.
 * @typedef {[string, boolean, string?]} Seg
 */
/**
 * A line of a change: its mark and the mark's class, what it says,
 * and its detail.
 * @typedef {{ k: string, cls: string, what: Seg[], p: string }} Part
 */

export function start() {
    const VALSET_POLL_MS = 30000
    const PAGE = 50
    // A read without an answer by then is given up, so a stalled
    // one never holds up the reads after it.
    const READ_BOUND_MS = 10000
    const VIEW_KEY = 'gnockpit-validators-view'
    const VIEWS = ['live', 'timeline', 'ranking']
    const card = $('validators-card')
    const list = $('validators-timeline')
    const seg = $('validators-view')
    const rankingButton = $in(seg, 'button[data-view="ranking"]')
    let view = 'live'
    // The card waits to show the Ranking view until the server's word
    // that the ranking is on; a view the viewer picks ends the wait.
    let rankingWaits = false
    /** @type {HistoryChange[]} */
    let changes = []
    let moreBelow = false // the history holds changes below the last page read
    // filled is how far the stored history reaches, as the last
    // answer of /api/history sent it, unchecked; null before one.
    /** @type {HistoryFilled | null} */
    let filled = null
    // asOf is the server's time the last answer read is as of, in
    // ms: the rows' ages read it, as the browser's clock may be
    // off.
    let asOf = 0
    let timer = 0
    let gen = 0 // the poll chain in force; a view change starts another
    let loadErr = '' // why the last read failed; '' when it did not
    let loadingOlder = false
    // "older" had the focus when its read began: disabling it
    // gave the focus to the page.
    let olderFocused = false
    // The key of the first name the last older read added: where
    // the focus on "older" goes when that read removes it. A read
    // that adds no name hands it to the list's last name.
    /** @type {string | null} */
    let firstAdded = null
    let reads = Promise.resolve() // the reads, in turn
    // nameKeys holds each name link's key, the same across
    // redraws: its change's height and its place among the
    // change's names.
    /** @type {WeakMap<Element, string>} */
    const nameKeys = new WeakMap()
    /**
     * @param {number} height
     * @param {number} nth
     * @returns {string}
     */
    function nameKey(height, nth) {
        return height + ':' + nth
    }

    // el builds an element of tag, with its class and its text when
    // given.
    /**
     * @template {keyof HTMLElementTagNameMap} K
     * @param {K} tag
     * @param {string | null} [cls]
     * @param {string | null} [text]
     * @returns {HTMLElementTagNameMap[K]}
     */
    function el(tag, cls, text) {
        const e = document.createElement(tag)
        if (cls) e.className = cls
        if (text != null) e.textContent = text
        return e
    }
    // who names member m: its moniker, else its address shortened.
    /**
     * @param {{ moniker?: string, address: string }} m
     * @returns {string}
     */
    function who(m) {
        return m.moniker || shortAddr(m.address)
    }
    // names lists members, their names in bold and linked to their
    // pages: [text, bold, address] segments.
    /**
     * @param {HistoryChangeMember[]} ms
     * @returns {Seg[]}
     */
    function names(ms) {
        /** @type {Seg[]} */
        const out = []
        ms.forEach(function (m, i) {
            if (i > 0) out.push([i === ms.length - 1 ? ' and ' : ', ', false])
            out.push([who(m), true, m.address])
        })
        return out
    }
    // powers writes the power of members ms.
    /**
     * @param {HistoryChangeMember[]} ms
     * @returns {string}
     */
    function powers(ms) {
        const first = present(ms[0])
        const same = ms.every(function (m) {
            return m.power === first.power
        })
        if (same) return 'power ' + first.power + (ms.length > 1 ? ' each' : '')
        return (
            'power ' +
            ms
                .map(function (m) {
                    return m.power
                })
                .join(', ')
        )
    }
    // parts splits a change into its lines: one per kind, and one
    // per member whose power changed or key rotated.
    /**
     * @param {HistoryChange} c
     * @returns {Part[]}
     */
    function parts(c) {
        if (c.genesis)
            return [
                {
                    k: '◆',
                    cls: 'vt-gen',
                    what: [['Genesis set', true]],
                    p: '',
                },
            ]
        /** @type {Part[]} */
        const out = []
        if (c.added.length)
            out.push({
                k: '+',
                cls: 'vt-add',
                what: names(c.added).concat([[' joined', false]]),
                p: powers(c.added),
            })
        if (c.removed.length)
            out.push({
                k: '−',
                cls: 'vt-rm',
                what: names(c.removed).concat([[' left', false]]),
                p: powers(c.removed),
            })
        c.repowered.forEach(function (m) {
            out.push({
                k: '↕',
                cls: 'vt-pow',
                what: [
                    [who(m), true, m.address],
                    [' power ' + m.before + ' → ' + m.after, false],
                ],
                p: '',
            })
        })
        c.rotated.forEach(function (r) {
            out.push({
                k: '⇄',
                cls: 'vt-rot',
                what: [
                    [
                        r.moniker || shortAddr(r.new_address),
                        true,
                        r.new_address,
                    ],
                    [' rotated its key', false],
                ],
                p: shortAddr(r.old_address) + ' → ' + shortAddr(r.new_address),
            })
        })
        return out
    }
    // size writes the set's size after change c, and before it when
    // it changed.
    /**
     * @param {HistoryChange} c
     * @returns {string}
     */
    function size(c) {
        if (c.size_before === c.size_after || c.genesis)
            return c.size_after + ' validators'
        return c.size_before + ' → ' + c.size_after + ' validators'
    }
    // rows builds the rows of change c, its height and age on the
    // first.
    /**
     * @param {HistoryChange} c
     * @returns {HTMLDivElement[]}
     */
    function rows(c) {
        let linked = 0 // the change's names linked so far
        return parts(c).map(function (part, i) {
            const r = el('div', 'vt-row')
            const h = el(
                'span',
                'h',
                i
                    ? ''
                    : '#' + thousands(c.height) + ' · ' + timeAgo(c.time, asOf)
            )
            h.setAttribute('data-popover', utcMinute(c.time))
            h.classList.add('tap-popover')
            r.appendChild(h)
            r.appendChild(el('span', 'k ' + part.cls, part.k))
            const w = el('span', 'w')
            part.what.forEach(function (seg) {
                if (seg[2]) {
                    const a = valLink(seg[2], el('b', null, seg[0]))
                    nameKeys.set(a, nameKey(c.height, linked++))
                    w.appendChild(a)
                } else
                    w.appendChild(
                        seg[1]
                            ? el('b', null, seg[0])
                            : document.createTextNode(seg[0])
                    )
            })
            if (part.p) {
                w.appendChild(document.createTextNode(' '))
                w.appendChild(el('span', 'p', part.p))
            }
            if (!i && c.events_pending)
                w.appendChild(el('span', 'vt-pend', 'events pending'))
            r.appendChild(w)
            r.appendChild(el('span', 'n', i ? '' : size(c)))
            return r
        })
    }
    function render() {
        // The footer is rebuilt: keep a keyboard user on "older".
        // Its read's disabling dropped the focus: "older" takes
        // it back only while nothing else took it.
        const at = document.activeElement
        const refocus =
            (olderFocused && (at === null || at === document.body)) ||
            (at !== null && at.classList.contains('vt-older'))
        // A keyboard user on a name stays on that name.
        const name =
            at !== null && list.contains(at) ? nameKeys.get(at) : undefined
        list.replaceChildren()
        changes.forEach(function (c) {
            rows(c).forEach(function (r) {
                list.appendChild(r)
            })
        })
        if (!changes.length && !loadErr)
            list.appendChild(
                el(
                    'div',
                    'vt-foot',
                    'No validator set change in the history yet'
                )
            )
        const foot = el('div', 'vt-foot')
        const f = filled
        const filling = f && f.to && !f.complete
        foot.appendChild(
            loadErr
                ? el('span', 'vt-err', loadErr)
                : el(
                      'span',
                      null,
                      filling
                          ? 'history reaches back to #' +
                                thousands(f.from) +
                                ': older changes appear as it fills'
                          : ''
                  )
        )
        /** @type {HTMLButtonElement | null} */
        let more = null
        // While the history fills, changes come below the last
        // one shown.
        if (changes.length && (moreBelow || filling)) {
            more = el('button', 'vt-older', 'older')
            more.disabled = loadingOlder
            more.addEventListener('click', older)
            foot.appendChild(more)
        }
        list.appendChild(foot)
        // Only an element in the document takes the focus. The
        // page stays where it was: the rows a read adds come in
        // above "older" and would scroll the page to it.
        if (more && refocus) more.focus({ preventScroll: true })
        else if (refocus && firstAdded !== null) focusName(firstAdded)
        else if (refocus) focusLastName()
        else if (name !== undefined) focusName(name)
    }
    function focusLastName() {
        const links = /** @type {NodeListOf<HTMLAnchorElement>} */ (
            list.querySelectorAll('a.val-link')
        )
        const last = links[links.length - 1]
        if (last) last.focus({ preventScroll: true })
    }
    // focusName focuses the name link whose key is key, when the
    // list still shows it.
    /** @param {string} key */
    function focusName(key) {
        const links = /** @type {NodeListOf<HTMLAnchorElement>} */ (
            list.querySelectorAll('a.val-link')
        )
        for (const a of links) {
            if (nameKeys.get(a) === key) {
                a.focus({ preventScroll: true })
                return
            }
        }
    }
    // reasonOf is the reason gnockpit gives in an error answer,
    // or null for an answer without one, as a proxy in front of
    // gnockpit sends.
    /**
     * @param {Response} r
     * @returns {Promise<string | null>}
     */
    async function reasonOf(r) {
        try {
            /** @type {unknown} */
            const body = await r.json()
            return errorOf(body)
        } catch (e) {
            // A body that is not JSON gives no reason.
        }
        return null
    }
    // load reads the page of changes below height before, the top
    // page without it. A failed read's error says whether the
    // server keeps no history: historyDisabled.
    /**
     * @param {number} [before]
     * @returns {Promise<HistoryReport>}
     */
    function load(before) {
        let q = '/api/history?changes=' + PAGE
        if (before) q += '&before=' + before
        return boundedRead(READ_BOUND_MS, async function (signal) {
            const r = await fetch(q, { signal })
            if (!r.ok) {
                const reason = await reasonOf(r)
                // gnockpit's own 503 is a server without a block
                // history: no later read can succeed. A proxy's
                // 503 passes.
                throw Object.assign(new Error(reason || 'HTTP ' + r.status), {
                    historyDisabled: r.status === 503 && reason !== null,
                })
            }
            /** @type {HistoryReport} */
            const rep = await r.json()
            return rep
        })
    }
    // refresh reads the changes shown again, page after page down
    // to the lowest one shown, so the rows show what the history
    // holds at each poll: the changes it adds at the top and
    // among the rows, and their events once read. The rows below
    // the lowest one shown stay for "older". A failed read keeps
    // the rows on screen and says so; the next poll tries again.
    // A refresh of a chain a view change ended reads no further
    // page.
    /**
     * @param {number} g
     * @returns {Promise<void>}
     */
    async function refresh(g) {
        if (g !== gen) return
        try {
            const lowest = lowestHeight(changes)
            let r = await load()
            let read = r.changes || []
            while (r.next_before && lowestHeight(read) > lowest) {
                if (g !== gen) return
                r = await load(r.next_before)
                read = read.concat(r.changes || [])
            }
            // A history refilled wholly below the rows shown
            // shows its first page: nothing is cut at row 0.
            const cut = read.findIndex(function (c) {
                return c.height < lowest
            })
            changes = cut > 0 ? read.slice(0, cut) : read
            moreBelow = cut > 0 || !!r.next_before
            filled = r.filled
            asOf = Date.parse(r.now)
            loadErr = ''
        } catch (e) {
            const disabled =
                e instanceof Error &&
                'historyDisabled' in e &&
                e.historyDisabled
            const retry = disabled ? '' : ': trying again shortly'
            loadErr =
                'Could not read the set changes (' +
                failureMessage(e) +
                ')' +
                retry +
                '.'
        }
        render()
    }
    // lowestHeight is the height of the lowest of changes, newest
    // first; Infinity for none.
    /**
     * @param {HistoryChange[]} list
     * @returns {number}
     */
    function lowestHeight(list) {
        const last = list.at(-1)
        return last ? last.height : Infinity
    }
    // inTurn runs read once the reads asked before it end, so a
    // read of the page below the rows starts from the rows the
    // read before it shows; it returns read's end.
    /**
     * @param {() => Promise<void>} read
     * @returns {Promise<void>}
     */
    function inTurn(read) {
        const done = reads.then(read)
        // The caller learns of a failure from done; the reads
        // after it run all the same.
        reads = done.catch(function () {})
        return done
    }
    // older reads the page below the rows, from a click on "older".
    /**
     * @param {MouseEvent} click
     * @returns {Promise<void>}
     */
    function older(click) {
        // render gives "older" this listener.
        const button = /** @type {HTMLButtonElement} */ (click.currentTarget)
        olderFocused = document.activeElement === button
        button.disabled = true
        loadingOlder = true
        return inTurn(readOlder)
    }
    // readOlder adds the page below the rows shown. A poll before
    // it can have emptied the list: then nothing is read.
    /** @returns {Promise<void>} */
    async function readOlder() {
        const last = changes.at(-1)
        if (last) {
            try {
                const r = await load(last.height)
                const page = r.changes || []
                changes = changes.concat(page)
                // The genesis set names no one.
                const named = page.find(function (c) {
                    return !c.genesis
                })
                firstAdded = named ? nameKey(named.height, 0) : null
                moreBelow = !!r.next_before
                filled = r.filled
                asOf = Date.parse(r.now)
                loadErr = ''
            } catch (e) {
                loadErr =
                    'Could not read older changes (' + failureMessage(e) + ').'
            }
        }
        loadingOlder = false
        render()
        olderFocused = false
        firstAdded = null
    }
    // poll keeps one chain of reads per showing of the view:
    // a view change starts another and ends the one before.
    /** @param {number} g */
    function poll(g) {
        inTurn(function () {
            return refresh(g)
        }).finally(function () {
            if (g === gen && view === 'timeline')
                timer = setTimeout(function () {
                    poll(g)
                }, VALSET_POLL_MS)
        })
    }
    // setView shows view v. It saves nothing: only the viewer's click
    // on the switch does, so neither ?view= nor a view the card shows
    // on its own changes the view saved.
    /** @param {string} v */
    function setView(v) {
        view = v
        card.classList.toggle('view-timeline', v === 'timeline')
        card.classList.toggle('view-ranking', v === 'ranking')
        seg.querySelectorAll('button').forEach(function (b) {
            b.setAttribute('aria-pressed', String(b.dataset.view === v))
        })
        clearTimeout(timer)
        gen++
        if (v === 'timeline') poll(gen)
        if (v === 'ranking') rankingShown()
        else rankingHidden()
    }
    // A click on the switch is the viewer's choice: it saves the view,
    // and takes ?view= out of the page's address, so a reload opens the
    // view clicked, not the link's. The entry is replaced, its state
    // kept: the validator page's entries carry theirs.
    seg.addEventListener('click', function (e) {
        if (!(e.target instanceof Element)) return
        const b = e.target.closest('button')
        if (!b) return
        rankingWaits = false
        // Each of the view's buttons carries data-view (index.html).
        const v = /** @type {string} */ (b.dataset.view)
        storageSet(VIEW_KEY, v)
        const url = new URL(location.href)
        if (url.searchParams.has('view')) {
            url.searchParams.delete('view')
            history.replaceState(history.state, '', url)
        }
        setView(v)
    })
    // A user who moves on while "older" reads keeps where they
    // went: a press outside the list, or the focus taken anywhere,
    // cancels giving the focus back to "older".
    document.addEventListener(
        'pointerdown',
        function (e) {
            if (!(e.target instanceof Node)) return
            if (!list.contains(e.target)) olderFocused = false
        },
        true
    )
    document.addEventListener(
        'focusin',
        function () {
            olderFocused = false
        },
        true
    )
    // The card opens on the view ?view= names, else on the one saved,
    // else on Live. The Ranking view waits for /api/boot's word:
    // Live shows meanwhile.
    const wanted =
        [
            new URLSearchParams(location.search).get('view'),
            storageGet(VIEW_KEY),
        ].find(function (v) {
            return v !== null && VIEWS.includes(v)
        }) || 'live'
    rankingWaits = wanted === 'ranking'
    setView(rankingWaits ? 'live' : wanted)
    offer = function (on) {
        rankingButton.hidden = !on
        if (on && rankingWaits) {
            rankingWaits = false
            setView('ranking')
        } else if (!on && view === 'ranking') {
            rankingWaits = true
            setView('live')
        }
    }
}
