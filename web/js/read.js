// boundedRead runs read(signal) and gives it up once ms have
// passed: signal aborts its requests, the reads of their bodies
// included. A read given up rejects with a TimeoutError "no
// answer in N s", whichever error the browser aborts it with, as
// Safari drops the abort's reason; any other failure rejects as
// it is. The bound's own timer tells a read given up from any
// other abort.
/**
 * @template T
 * @param {number} ms
 * @param {(signal: AbortSignal) => Promise<T>} read
 * @returns {Promise<T>}
 */
export async function boundedRead(ms, read) {
    const abort = new AbortController()
    let passed = false
    const timer = setTimeout(() => {
        passed = true
        abort.abort()
    }, ms)
    try {
        return await read(abort.signal)
    } catch (e) {
        if (!passed) throw e
        throw new DOMException(`no answer in ${ms / 1000} s`, 'TimeoutError')
    } finally {
        clearTimeout(timer)
    }
}

// failureMessage is the message of failure e: an Error's, or a
// DOMException's such as a read given up; e as text otherwise.
/**
 * @param {unknown} e
 * @returns {string}
 */
export function failureMessage(e) {
    return e instanceof Error || e instanceof DOMException
        ? e.message
        : String(e)
}

// errorOf is the error an answer's JSON body names, or null for a
// body that is no object or names no text as its error.
/**
 * @param {unknown} body
 * @returns {string | null}
 */
export function errorOf(body) {
    if (typeof body !== 'object' || body === null || !('error' in body))
        return null
    return typeof body.error === 'string' && body.error ? body.error : null
}
