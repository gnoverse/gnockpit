// The country a point of the location form's map lies in: the country
// borders gnockpit serves (tools/countryborders writes them from Natural
// Earth), read until a read succeeds and kept for the page's life, and the
// lookup in them.

/** @import { CountryBorders } from '../types/borders.js' */

import { present } from './present.js'
import { boundedRead, failureMessage } from './read.js'

// BORDERS_READ_BOUND_MS bounds the read of the borders, about 400 KB:
// one without an answer by then is given up, as one that failed.
const BORDERS_READ_BOUND_MS = 30000

/**
 * The borders, by country code, once read.
 * @type {Map<string, number[][]> | null}
 */
let borders = null

/**
 * The read of the borders, once started.
 * @type {Promise<void> | null}
 */
let reading = null

// readBorders starts the read of the borders the first time it is called,
// and returns it: it settles once the borders are read, or once the read
// failed, which it warns of and forgets, so that the next call reads them
// again. An answer that holds no country borders (see bordersOf) is a
// read that failed.
/** @returns {Promise<void>} */
export function readBorders() {
    if (reading === null) {
        reading = boundedRead(BORDERS_READ_BOUND_MS, async function (signal) {
            const res = await fetch('/data/country-borders.json', { signal })
            if (!res.ok) throw new Error('HTTP ' + res.status)
            return bordersOf(await res.json())
        }).then(
            function (read) {
                borders = read
            },
            /** @param {unknown} e */
            function (e) {
                console.warn('read the country borders:', failureMessage(e))
                reading = null
            }
        )
    }
    return reading
}

// bordersOf is the borders an answer holds, by country code: an object
// whose every value is a list of rings, each a list; it throws for any
// other answer, which countryAt could not read.
/**
 * @param {unknown} answer
 * @returns {Map<string, number[][]>}
 */
function bordersOf(answer) {
    if (
        typeof answer !== 'object' ||
        answer === null ||
        Array.isArray(answer) ||
        !Object.values(answer).every(
            (rings) => Array.isArray(rings) && rings.every(Array.isArray)
        )
    )
        throw new Error('the answer holds no country borders')
    /** @type {CountryBorders} */
    const read = /** @type {CountryBorders} */ (answer)
    return new Map(Object.entries(read))
}

// countryOf is the country the point (lat, lon) lies in (see countryAt),
// null until the borders are read.
/**
 * @param {number} lat
 * @param {number} lon
 * @returns {string | null}
 */
export function countryOf(lat, lon) {
    return borders === null ? null : countryAt(borders, lat, lon)
}

// countryAt is the code of the one country of borders whose rings hold the
// point (lat, lon), rounded to the 0.1° grid; null for a point two hold,
// where borders rounded to the grid overlap. A ray from the point crossing
// a country's rings an odd number of times lies in it, so that a point in
// a hole (Lesotho, in South Africa) does not. The arithmetic is on the
// grid's integers, exact: a point on a border two countries share, which
// tools/countryborders keeps alike in both, lies in one of them. A point no
// country holds, which the ray counts out of a
// country's northern or eastern edge, lies in the one country whose edge it
// lies on (a coast, or most of a country a few grid steps wide, as
// Singapore); null when it lies on none (the sea) or on two.
/**
 * @param {Map<string, number[][]>} borders
 * @param {number} lat
 * @param {number} lon
 * @returns {string | null}
 */
export function countryAt(borders, lat, lon) {
    const x = Math.round(lon * 10)
    const y = Math.round(lat * 10)
    const inside = countriesWhere(borders, (rings) => holds(rings, x, y))
    if (inside.length > 0)
        return inside.length === 1 ? present(inside[0]) : null
    const along = countriesWhere(borders, (rings) => touches(rings, x, y))
    return along.length === 1 ? present(along[0]) : null
}

// countriesWhere is the codes of the countries of borders whose rings
// satisfy test, stopping at two: one more tells nothing countryAt uses.
/**
 * @param {Map<string, number[][]>} borders
 * @param {(rings: number[][]) => boolean} test
 * @returns {string[]}
 */
function countriesWhere(borders, test) {
    /** @type {string[]} */
    const codes = []
    for (const [code, rings] of borders) {
        if (!test(rings)) continue
        codes.push(code)
        if (codes.length === 2) break
    }
    return codes
}

// touches tells whether the point (x, y) lies on an edge of rings, flat
// lists of x, y pairs: on the line through its ends, between them.
/**
 * @param {number[][]} rings
 * @param {number} x
 * @param {number} y
 * @returns {boolean}
 */
function touches(rings, x, y) {
    for (const ring of rings) {
        for (let i = 0, j = ring.length - 2; i < ring.length; j = i, i += 2) {
            const xi = present(ring[i])
            const yi = present(ring[i + 1])
            const xj = present(ring[j])
            const yj = present(ring[j + 1])
            if ((xj - xi) * (y - yi) !== (yj - yi) * (x - xi)) continue
            if (x < Math.min(xi, xj) || x > Math.max(xi, xj)) continue
            if (y < Math.min(yi, yj) || y > Math.max(yi, yj)) continue
            return true
        }
    }
    return false
}

// holds tells whether rings, flat lists of x, y pairs, hold the point (x,
// y): whether a ray from it towards greater x crosses their edges an odd
// number of times. An edge counts its lower end and not its upper one, so a
// ray through a vertex crosses once, and a horizontal edge never.
/**
 * @param {number[][]} rings
 * @param {number} x
 * @param {number} y
 * @returns {boolean}
 */
function holds(rings, x, y) {
    let inside = false
    for (const ring of rings) {
        for (let i = 0, j = ring.length - 2; i < ring.length; j = i, i += 2) {
            const xi = present(ring[i])
            const yi = present(ring[i + 1])
            const xj = present(ring[j])
            const yj = present(ring[j + 1])
            if (yi > y === yj > y) continue
            // The ray crosses the edge when x lies before the edge's x at
            // y: x - xi < (xj - xi) * (y - yi) / (yj - yi), multiplied out
            // by yj - yi, which flips the comparison when it is negative.
            const dy = yj - yi
            const before = (x - xi) * dy
            const edge = (xj - xi) * (y - yi)
            if (dy > 0 ? before < edge : before > edge) inside = !inside
        }
    }
    return inside
}
