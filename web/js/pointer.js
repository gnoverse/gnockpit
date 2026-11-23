// pointerX and pointerY are where the pointer last moved, so a
// redraw can tell what it is on: pointedElement.
let pointerX = -1
let pointerY = -1
/** @returns {Element | null} */
export function pointedElement() {
    return document.elementFromPoint(pointerX, pointerY)
}

export function start() {
    document.addEventListener(
        'mousemove',
        function (e) {
            pointerX = e.clientX
            pointerY = e.clientY
        },
        { passive: true }
    )
}
