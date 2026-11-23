/**
 * startEach calls each feature's start in turn. A start that throws is
 * logged with its feature's name and left; the next one still runs, so a
 * broken feature leaves the rest of the page running.
 * @param {Array<[string, () => void]>} starts each feature's name and start,
 *   in the order they run
 */
export function startEach(starts) {
    for (const [name, start] of starts) {
        try {
            start()
        } catch (err) {
            console.error(`${name} did not start:`, err)
        }
    }
}
