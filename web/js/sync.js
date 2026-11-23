// Network State's header shows the block history's fill while it
// runs, from /api/history: the share filled, and the time left at
// the fill's rate once the server has measured it. A read that
// fails, or gets no answer in 10 s, leaves the line as it shows.

/** @import { HistoryFilled, HistoryReport } from '../types/history.js' */

import { $, $in, mkEl } from './dom.js'
import { MONTHS, thousands, twoDigits } from './format.js'
import { popRows } from './popover.js'
import { boundedRead } from './read.js'

export function start() {
    const SYNC_POLL_MS = 10000
    // SYNC_READ_BOUND_MS bounds a read: one without an answer by
    // then is given up, as a read that failed.
    const SYNC_READ_BOUND_MS = 10000
    // day writes RFC 3339 time iso to the minute, its month by name.
    /**
     * @param {string} iso
     * @returns {string}
     */
    function day(iso) {
        const d = new Date(iso)
        const hh = twoDigits(d.getUTCHours())
        const mm = twoDigits(d.getUTCMinutes())
        return (
            d.getUTCDate() +
            ' ' +
            MONTHS[d.getUTCMonth()] +
            ' ' +
            d.getUTCFullYear() +
            ' ' +
            hh +
            ':' +
            mm +
            ' UTC'
        )
    }
    // timeLeft writes s seconds roughly, in their largest units.
    /**
     * @param {number} s
     * @returns {string}
     */
    function timeLeft(s) {
        const m = Math.round(s / 60)
        if (m < 1) return '< 1 min'
        if (m < 60) return '≈ ' + m + ' min'
        const h = Math.floor(m / 60)
        if (h >= 48) return '≈ ' + Math.round(h / 24) + ' d'
        if (h >= 10) return '≈ ' + h + ' h'
        return '≈ ' + h + ' h ' + twoDigits(m % 60) + ' min'
    }
    // show writes the fill f on the line, hidden when the history is
    // complete or holds no block.
    /** @param {HistoryFilled} f */
    function show(f) {
        const el = $('ns-sync')
        el.hidden = !f || !f.to || f.complete
        if (el.hidden) return
        const lowest = Math.max(1, f.floor || 0)
        const pct = ((f.to - f.from + 1) / (f.to - lowest + 1)) * 100
        $in(el, '.fill').style.width = Math.max(pct, 0.5) + '%'
        // The share in tenths of a percent, rounded down from
        // whole heights: it reads 10% only once it is.
        const tenths = Math.floor(
            ((f.to - f.from + 1) * 1000) / (f.to - lowest + 1)
        )
        const share =
            tenths < 100
                ? (tenths / 10).toFixed(1)
                : String(Math.floor(tenths / 10))
        /** @type {Array<Node | string>} */
        const text = [mkEl('b', share + '%')]
        if (f.rate !== undefined && f.rate > 0)
            text.push(
                ' · ',
                mkEl('b', timeLeft((f.from - lowest) / f.rate)),
                ' left'
            )
        $in(el, '.text').replaceChildren(...text)
        popRows(el, 'Block history', [
            [
                null,
                'filled from',
                '#' +
                    thousands(f.from) +
                    ' (' +
                    // A history holding a block has the time of its
                    // lowest (blocks.Progress).
                    day(/** @type {string} */ (f.from_time)) +
                    ')',
            ],
            [null, 'to', '#' + thousands(f.to)],
            [null, 'filling back to', '#' + thousands(lowest)],
        ])
    }
    ;(async function poll() {
        try {
            await boundedRead(SYNC_READ_BOUND_MS, async function (signal) {
                const res = await fetch('/api/history', {
                    signal,
                })
                if (!res.ok) return
                /** @type {HistoryReport} */
                const rep = await res.json()
                show(rep.filled)
            })
        } catch (e) {
            // A failed or given-up read leaves the line as it
            // shows: the next read that answers updates it.
        }
        setTimeout(poll, SYNC_POLL_MS)
    })()
}
