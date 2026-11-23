// present is the value an indexed read found. It stands where the read
// cannot find nothing: by the bounds of a loop, by a check made before
// the read, or by the shape of the data (a matched pattern's groups,
// a fixed list). It throws when it does find nothing, so a miss that
// should not happen is never taken for a value.
/**
 * @template T
 * @param {T | undefined} value
 * @returns {T}
 */
export function present(value) {
    if (value === undefined) throw new Error('an indexed read found nothing')
    return value
}
