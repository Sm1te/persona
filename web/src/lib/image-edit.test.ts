import { describe, expect, it } from "vitest";
import {
  applyMatrix,
  cropRect,
  dragCrop,
  handleAt,
  isEmpty,
  isFramedAsIs,
  keepMark,
  layout,
  markWidth,
  noEdits,
  noHistory,
  record,
  redo,
  renderPlan,
  resetFrame,
  reshape,
  sameEdits,
  screenToPixel,
  turnLeft,
  turnMatrix,
  turnPoint,
  turnedSize,
  undo,
  unturnPoint,
  type ImageEdits,
  type Mark,
  type Rect,
} from "./image-edit";

// The editor's edits as iOS keeps them (apple/PersonaTests/ImageEditsTests.swift, the same expected values): turning,
// cropping and marks map points and pixels where they belong, a crop handle keeps the crop inside the picture and in
// its shape, and Undo and Redo step through whole edits.

const size = { width: 40, height: 30 };
const edits = (over: Partial<ImageEdits> = {}): ImageEdits => ({ ...noEdits(), ...over });
const close = (a: Rect, b: Rect) => {
  for (const k of ["x", "y", "width", "height"] as const) expect(a[k], k).toBeCloseTo(b[k], 9);
};
const pen = (points = [{ x: 0, y: 0 }, { x: 1, y: 1 }]): Mark => ({ tool: "pen", ink: "red", width: 2, points });

describe("turning", () => {
  it("brings a point back from every turn, and the transform agrees", () => {
    const points = [{ x: 0, y: 0 }, { x: 40, y: 0 }, { x: 13, y: 7 }, { x: 40, y: 30 }];
    for (let turns = 0; turns < 4; turns++) {
      const e = edits({ turns });
      for (const p of points) {
        const back = unturnPoint(e, turnPoint(e, p, size), size);
        expect(back.x).toBeCloseTo(p.x, 9);
        expect(back.y).toBeCloseTo(p.y, 9);
        expect(applyMatrix(turnMatrix(e, size), p)).toEqual(turnPoint(e, p, size));
      }
    }
  });

  it("puts the top left at the top right after a quarter clockwise", () => {
    const e = edits({ turns: 1 });
    expect(turnedSize(e, size)).toEqual({ width: 30, height: 40 });
    expect(turnPoint(e, { x: 0, y: 0 }, size)).toEqual({ x: 30, y: 0 });
    expect(turnPoint(e, { x: 40, y: 0 }, size)).toEqual({ x: 30, y: 40 });
  });

  it("takes the crop and its shape along when turning left, and four turns come back", () => {
    let e = edits({ crop: { x: 0.1, y: 0.2, width: 0.5, height: 0.3 }, shape: "4:3" });
    e = turnLeft(e);
    expect(e.turns).toBe(3);
    expect(e.shape).toBe("3:4");
    close(e.crop, { x: 0.2, y: 0.4, width: 0.3, height: 0.5 });
    for (let i = 0; i < 3; i++) e = turnLeft(e);
    expect(e.turns).toBe(0);
    expect(e.shape).toBe("4:3");
    close(e.crop, { x: 0.1, y: 0.2, width: 0.5, height: 0.3 });
  });

  it("keeps the marks on Reset", () => {
    const e = resetFrame(edits({ turns: 2, crop: { x: 0, y: 0, width: 0.5, height: 0.5 }, shape: "square", marks: [pen()] }));
    expect(isFramedAsIs(e)).toBe(true);
    expect(e.marks).toHaveLength(1);
    expect(isEmpty(e)).toBe(false);
  });
});

describe("shapes and handles", () => {
  it("makes a square on a wide picture as tall as it and in the middle; Freeform keeps the crop", () => {
    let e = reshape(noEdits(), "square", size); // 40 × 30
    close(e.crop, { x: 0.125, y: 0, width: 0.75, height: 1 });
    expect(cropRect(e, size)).toEqual({ x: 5, y: 0, width: 30, height: 30 });
    e = reshape(e, "free", size);
    expect(e.shape).toBe("free");
    close(e.crop, { x: 0.125, y: 0, width: 0.75, height: 1 });
  });

  it("measures Original on the turned picture", () => {
    const e = reshape(edits({ turns: 1, crop: { x: 0, y: 0, width: 1, height: 0.5 } }), "original", size); // turned: 30 × 40
    close(e.crop, { x: 0, y: 0, width: 1, height: 1 });
  });

  it("keeps a free corner inside and no smaller than the least", () => {
    const start = { x: 0.2, y: 0.2, width: 0.5, height: 0.5 };
    const least = { width: 0.1, height: 0.1 };
    close(dragCrop(start, "topLeft", { x: -0.5, y: -0.5 }, least, null), { x: 0, y: 0, width: 0.7, height: 0.7 });
    close(dragCrop(start, "bottomRight", { x: -0.9, y: -0.9 }, least, null), { x: 0.2, y: 0.2, width: 0.1, height: 0.1 });
    close(dragCrop(start, "right", { x: 0.1, y: 0.3 }, least, null), { x: 0.2, y: 0.2, width: 0.6, height: 0.5 });
  });

  it("keeps a fixed shape, the opposite corner staying put; an edge grows round its middle", () => {
    const start = { x: 0.3, y: 0.3, width: 0.4, height: 0.4 };
    const least = { width: 0.1, height: 0.1 };
    close(dragCrop(start, "bottomRight", { x: 0.2, y: 0.05 }, least, 1), { x: 0.3, y: 0.3, width: 0.6, height: 0.6 });
    close(dragCrop(start, "topLeft", { x: -0.9, y: -0.1 }, least, 1), { x: 0, y: 0, width: 0.7, height: 0.7 });
    const side = dragCrop(start, "right", { x: 0.2, y: 0 }, least, 1);
    expect(side.width).toBeCloseTo(side.height, 9);
    expect(side.x).toBe(0.3);
    expect(side.y + side.height / 2).toBeCloseTo(0.5, 9);
  });

  it("moves the crop from inside, not past the edge", () => {
    close(dragCrop({ x: 0.2, y: 0.2, width: 0.5, height: 0.5 }, "inside", { x: 0.6, y: -0.1 }, { width: 0, height: 0 }, null), { x: 0.5, y: 0.1, width: 0.5, height: 0.5 });
  });

  it("gives the pointer the corner, the edge or the inside", () => {
    const frame = { x: 100, y: 100, width: 200, height: 100 };
    expect(handleAt({ x: 95, y: 104 }, frame)).toBe("topLeft");
    expect(handleAt({ x: 305, y: 196 }, frame)).toBe("bottomRight");
    expect(handleAt({ x: 200, y: 92 }, frame)).toBe("top");
    expect(handleAt({ x: 299, y: 150 }, frame)).toBe("right");
    expect(handleAt({ x: 200, y: 150 }, frame)).toBe("inside");
    expect(handleAt({ x: 20, y: 20 }, frame)).toBeNull();
  });
});

describe("on screen", () => {
  const area = { width: 400, height: 400 };

  it("fills the room with the crop while marking, and shows all of the turned picture, inset, while cropping", () => {
    const e = edits({ crop: { x: 0.5, y: 0, width: 0.5, height: 1 } }); // the right half of 40 × 30: 20 × 30
    const marking = layout(e, size, area, false);
    expect(marking.visible.height).toBeCloseTo(400, 9);
    expect(marking.visible.width).toBeCloseTo((400 * 20) / 30, 9);
    // the picture runs off to the left by the half cropped away
    expect(marking.picture.x).toBeCloseTo(marking.visible.x - 20 * marking.scale, 9);
    const cropping = layout(e, size, area, true);
    expect(cropping.picture.width).toBeCloseTo(400 - 56, 9);
    expect(cropping.crop.x).toBeCloseTo(cropping.picture.x + cropping.picture.width / 2, 9);
  });

  it("maps the pointer into the turned picture's pixels, held inside what shows", () => {
    const l = layout(noEdits(), size, { width: 400, height: 300 }, false); // 10 screen pixels a picture pixel
    expect(l.scale).toBeCloseTo(10, 9);
    expect(screenToPixel(l, { x: 135, y: 72 })).toEqual({ x: 13.5, y: 7.2 });
    expect(screenToPixel(l, { x: -50, y: 900 })).toEqual({ x: 0, y: 30 });
  });

  it("burns a line in as wide, relative to the picture, as it was seen", () => {
    expect(markWidth(3.5, 0.5)).toBe(7);
    expect(markWidth(7, 2)).toBe(3.5);
  });

  it("keeps a drag, not a tap", () => {
    expect(keepMark(pen([{ x: 1, y: 1 }]))).toBe(false);
    expect(keepMark(pen())).toBe(true);
    expect(keepMark({ tool: "box", ink: "red", width: 2, points: [{ x: 1, y: 1 }, { x: 1.5, y: 1.5 }] })).toBe(false);
    expect(keepMark({ tool: "box", ink: "red", width: 2, points: [{ x: 1, y: 1 }, { x: 9, y: 1.5 }] })).toBe(true);
  });
});

describe("drawing the result", () => {
  // where a source pixel's middle lands in the result, as a whole pixel
  const lands = (e: ImageEdits, s: { width: number; height: number }, x: number, y: number) => {
    const plan = renderPlan(e, s)!;
    const p = applyMatrix(plan.matrix, { x: x + 0.5, y: y + 0.5 });
    return [Math.floor(p.x), Math.floor(p.y)];
  };

  it("draws a turn's pixels where they go", () => {
    const two = { width: 2, height: 1 }; // red, blue
    const clockwise = renderPlan(edits({ turns: 1 }), two)!;
    expect([clockwise.width, clockwise.height]).toEqual([1, 2]);
    expect(lands(edits({ turns: 1 }), two, 0, 0)).toEqual([0, 0]); // turned clockwise, the left side is on top
    expect(lands(edits({ turns: 1 }), two, 1, 0)).toEqual([0, 1]);
    expect(lands(edits({ turns: 3 }), two, 1, 0)).toEqual([0, 0]); // turned left, the right side is on top
  });

  it("keeps a crop's own pixels", () => {
    const four = { width: 4, height: 1 }; // red, blue, green, red
    const e = edits({ crop: { x: 0.25, y: 0, width: 0.5, height: 1 } });
    const plan = renderPlan(e, four)!;
    expect([plan.width, plan.height]).toEqual([2, 1]);
    expect(lands(e, four, 1, 0)).toEqual([0, 0]); // blue
    expect(lands(e, four, 2, 0)).toEqual([1, 0]); // green
  });

  it("burns a mark in where it was drawn after a turn", () => {
    // turned clockwise, the source's (2.5, 4.5) is at (10 - 4.5, 2.5)
    const plan = renderPlan(edits({ turns: 1 }), { width: 10, height: 10 })!;
    expect(applyMatrix(plan.matrix, { x: 2.5, y: 4.5 })).toEqual({ x: 5.5, y: 2.5 });
  });

  it("leaves nothing to draw when the crop is empty", () => {
    expect(renderPlan(edits({ crop: { x: 0.5, y: 0.5, width: 0, height: 0 } }), size)).toBeNull();
  });
});

describe("undo and redo", () => {
  it("step through whole edits, and a new change leaves nothing to redo", () => {
    let history = noHistory();
    const a = noEdits(),
      b = edits({ turns: 3 }),
      c = edits({ turns: 2 });
    history = record(history, a); // a → b
    history = record(history, b); // b → c
    let now = c;
    let step = undo(history, now)!;
    ({ history, edits: now } = step);
    expect(now).toBe(b);
    expect(history.future).toHaveLength(1);
    step = undo(history, now)!;
    ({ history, edits: now } = step);
    expect(now).toBe(a);
    expect(history.past).toHaveLength(0);
    expect(undo(history, now)).toBeNull();
    ({ history, edits: now } = redo(history, now)!);
    expect(now).toBe(b);
    history = record(history, now); // a new change
    expect(history.future).toHaveLength(0);
    expect(history.past).toHaveLength(2);
  });

  it("tells a change from none", () => {
    const m = pen();
    const a = edits({ marks: [m] });
    expect(sameEdits(a, edits({ marks: [m] }))).toBe(true);
    expect(sameEdits(a, edits({ marks: [pen()] }))).toBe(false);
    expect(sameEdits(a, { ...a, crop: { ...a.crop, width: 0.9 } })).toBe(false);
    expect(sameEdits(a, turnLeft(turnLeft(turnLeft(turnLeft(a)))))).toBe(true);
  });
});
