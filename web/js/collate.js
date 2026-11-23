// The order of names, and of the values of a sorted column.

// compareNames orders names alphabetically in English, case aside.
export const compareNames = new Intl.Collator('en').compare
// compareSortValues orders two values of a sorted column, isName
// for names: a missing value (null) last either way, names by
// compareNames, others by < and >. compareNames reads each value as
// text, as the collator would.
/**
 * @param {string | number | null} va
 * @param {string | number | null} vb
 * @param {boolean} isName
 * @param {boolean} asc
 * @returns {number}
 */
export function compareSortValues(va, vb, isName, asc) {
    if (va === null || vb === null) return va === vb ? 0 : va === null ? 1 : -1
    const c = isName
        ? compareNames(String(va), String(vb))
        : va < vb
          ? -1
          : va > vb
            ? 1
            : 0
    return asc ? c : -c
}
