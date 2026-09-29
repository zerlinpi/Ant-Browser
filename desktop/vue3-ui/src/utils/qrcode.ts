/**
 * Minimal QR Code encoder (ISO/IEC 18004) for short text such as otpauth://
 * URIs: byte mode, versions 1-40 chosen automatically, error correction raised
 * as far as the chosen version allows, and the mask picked by the standard
 * penalty rules. It follows the structure of Project Nayuki's QR Code
 * generator (MIT License).
 */

export type QrErrorCorrection = "L" | "M" | "Q" | "H";

export interface QrCode {
  version: number;
  size: number;
  errorCorrection: QrErrorCorrection;
  mask: number;
  /** modules[y][x] is true for a dark module. */
  modules: boolean[][];
}

const LEVELS: QrErrorCorrection[] = ["L", "M", "Q", "H"];
// Format information value of each level.
const FORMAT_BITS: Record<QrErrorCorrection, number> = { L: 1, M: 0, Q: 3, H: 2 };

// Indexed by version (index 0 unused).
const ECC_CODEWORDS_PER_BLOCK: Record<QrErrorCorrection, number[]> = {
  L: [-1, 7, 10, 15, 20, 26, 18, 20, 24, 30, 18, 20, 24, 26, 30, 22, 24, 28, 30, 28, 28, 28, 28, 30, 30, 26, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30],
  M: [-1, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26, 30, 22, 22, 24, 24, 28, 28, 26, 26, 26, 26, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28],
  Q: [-1, 13, 22, 18, 26, 18, 24, 18, 22, 20, 24, 28, 26, 24, 20, 30, 24, 28, 28, 26, 30, 28, 30, 30, 30, 30, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30],
  H: [-1, 17, 28, 22, 16, 22, 28, 26, 26, 24, 28, 24, 28, 22, 24, 24, 30, 28, 28, 26, 28, 30, 24, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30],
};
const ERROR_CORRECTION_BLOCKS: Record<QrErrorCorrection, number[]> = {
  L: [-1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 4, 4, 4, 4, 4, 6, 6, 6, 6, 7, 8, 8, 9, 9, 10, 12, 12, 12, 13, 14, 15, 16, 17, 18, 19, 19, 20, 21, 22, 24, 25],
  M: [-1, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5, 5, 8, 9, 9, 10, 10, 11, 13, 14, 16, 17, 17, 18, 20, 21, 23, 25, 26, 28, 29, 31, 33, 35, 37, 38, 40, 43, 45, 47, 49],
  Q: [-1, 1, 1, 2, 2, 4, 4, 6, 6, 8, 8, 8, 10, 12, 16, 12, 17, 16, 18, 21, 20, 23, 23, 25, 27, 29, 34, 34, 35, 38, 40, 43, 45, 48, 51, 53, 56, 59, 62, 65, 68],
  H: [-1, 1, 1, 2, 4, 4, 4, 5, 6, 8, 8, 11, 11, 16, 16, 18, 16, 19, 21, 25, 25, 25, 34, 30, 32, 35, 37, 40, 42, 45, 48, 51, 54, 57, 60, 63, 66, 70, 74, 77, 81],
};

const PENALTY_RUN = 3;
const PENALTY_BLOCK = 3;
const PENALTY_FINDER = 40;
const PENALTY_BALANCE = 10;

/** Modules available for data and error correction codewords. */
const rawDataModules = (version: number): number => {
  let result = (16 * version + 128) * version + 64;
  if (version >= 2) {
    const alignments = Math.floor(version / 7) + 2;
    result -= (25 * alignments - 10) * alignments - 55;
    if (version >= 7) result -= 36;
  }
  return result;
};

const dataCodewords = (version: number, level: QrErrorCorrection): number =>
  Math.floor(rawDataModules(version) / 8) - ECC_CODEWORDS_PER_BLOCK[level][version] * ERROR_CORRECTION_BLOCKS[level][version];

/** Width of the byte-mode character count field. */
const countBits = (version: number): number => (version <= 9 ? 8 : 16);

/** Multiplication in GF(2^8) modulo x^8 + x^4 + x^3 + x^2 + 1. */
const gfMultiply = (x: number, y: number): number => {
  let product = 0;
  for (let bit = 7; bit >= 0; bit--) {
    product = (product << 1) ^ ((product >>> 7) * 0x11d);
    product ^= ((y >>> bit) & 1) * x;
  }
  return product;
};

const reedSolomonDivisor = (degree: number): number[] => {
  const result = new Array<number>(degree).fill(0);
  result[degree - 1] = 1;
  let root = 1;
  for (let i = 0; i < degree; i++) {
    for (let j = 0; j < result.length; j++) {
      result[j] = gfMultiply(result[j], root);
      if (j + 1 < result.length) result[j] ^= result[j + 1];
    }
    root = gfMultiply(root, 0x02);
  }
  return result;
};

const reedSolomonRemainder = (data: number[], divisor: number[]): number[] => {
  const result = divisor.map(() => 0);
  for (const byte of data) {
    const factor = byte ^ (result.shift() ?? 0);
    result.push(0);
    divisor.forEach((coefficient, index) => {
      result[index] ^= gfMultiply(coefficient, factor);
    });
  }
  return result;
};

const alignmentPositions = (version: number, size: number): number[] => {
  if (version === 1) return [];
  const count = Math.floor(version / 7) + 2;
  const step = Math.floor((version * 8 + count * 3 + 5) / (count * 4 - 4)) * 2;
  const result = [6];
  for (let position = size - 7; result.length < count; position -= step) result.splice(1, 0, position);
  return result;
};

const maskApplies = (mask: number, x: number, y: number): boolean => {
  switch (mask) {
    case 0: return (x + y) % 2 === 0;
    case 1: return y % 2 === 0;
    case 2: return x % 3 === 0;
    case 3: return (x + y) % 3 === 0;
    case 4: return (Math.floor(x / 3) + Math.floor(y / 2)) % 2 === 0;
    case 5: return ((x * y) % 2) + ((x * y) % 3) === 0;
    case 6: return (((x * y) % 2) + ((x * y) % 3)) % 2 === 0;
    default: return (((x + y) % 2) + ((x * y) % 3)) % 2 === 0;
  }
};

/**
 * Encodes text (as UTF-8) at the given minimum error correction level. The
 * mask is chosen automatically unless `forcedMask` (0-7) is given.
 */
export const encodeQr = (text: string, minimum: QrErrorCorrection = "M", forcedMask?: number): QrCode => {
  const bytes = Array.from(new TextEncoder().encode(text));
  const usedBits = (version: number) => 4 + countBits(version) + bytes.length * 8;
  let version = 1;
  while (usedBits(version) > dataCodewords(version, minimum) * 8) {
    if (++version > 40) throw new RangeError("Text is too long for a QR code");
  }
  // Use the strongest error correction that still fits this version.
  let level = minimum;
  for (const candidate of LEVELS.slice(LEVELS.indexOf(minimum) + 1)) {
    if (usedBits(version) <= dataCodewords(version, candidate) * 8) level = candidate;
  }

  // Data codewords: mode, count, payload, terminator, bit padding, pad bytes.
  const bits: number[] = [];
  const append = (value: number, length: number) => {
    for (let i = length - 1; i >= 0; i--) bits.push((value >>> i) & 1);
  };
  const capacity = dataCodewords(version, level) * 8;
  append(0b0100, 4);
  append(bytes.length, countBits(version));
  for (const byte of bytes) append(byte, 8);
  append(0, Math.min(4, capacity - bits.length));
  append(0, (8 - (bits.length % 8)) % 8);
  for (let pad = 0xec; bits.length < capacity; pad ^= 0xec ^ 0x11) append(pad, 8);
  const data: number[] = [];
  for (let i = 0; i < bits.length; i += 8) {
    let byte = 0;
    for (let j = 0; j < 8; j++) byte = (byte << 1) | bits[i + j];
    data.push(byte);
  }

  // Split into blocks, add Reed-Solomon codewords and interleave.
  const blockCount = ERROR_CORRECTION_BLOCKS[level][version];
  const eccLength = ECC_CODEWORDS_PER_BLOCK[level][version];
  const rawCodewords = Math.floor(rawDataModules(version) / 8);
  const shortBlocks = blockCount - (rawCodewords % blockCount);
  const shortLength = Math.floor(rawCodewords / blockCount);
  const divisor = reedSolomonDivisor(eccLength);
  const blocks: number[][] = [];
  for (let index = 0, offset = 0; index < blockCount; index++) {
    const block = data.slice(offset, offset + shortLength - eccLength + (index < shortBlocks ? 0 : 1));
    offset += block.length;
    const ecc = reedSolomonRemainder(block, divisor);
    if (index < shortBlocks) block.push(0);
    blocks.push(block.concat(ecc));
  }
  const codewords: number[] = [];
  for (let i = 0; i < blocks[0].length; i++) {
    blocks.forEach((block, index) => {
      // Short blocks carry a placeholder where long blocks have one more data codeword.
      if (i !== shortLength - eccLength || index >= shortBlocks) codewords.push(block[i]);
    });
  }

  // Function patterns.
  const size = version * 4 + 17;
  const modules = Array.from({ length: size }, () => new Array<boolean>(size).fill(false));
  const reserved = Array.from({ length: size }, () => new Array<boolean>(size).fill(false));
  const setFunction = (x: number, y: number, dark: boolean) => {
    modules[y][x] = dark;
    reserved[y][x] = true;
  };
  for (let i = 0; i < size; i++) {
    setFunction(6, i, i % 2 === 0);
    setFunction(i, 6, i % 2 === 0);
  }
  const drawFinder = (centerX: number, centerY: number) => {
    for (let dy = -4; dy <= 4; dy++) {
      for (let dx = -4; dx <= 4; dx++) {
        const distance = Math.max(Math.abs(dx), Math.abs(dy));
        const x = centerX + dx;
        const y = centerY + dy;
        if (x >= 0 && x < size && y >= 0 && y < size) setFunction(x, y, distance !== 2 && distance !== 4);
      }
    }
  };
  drawFinder(3, 3);
  drawFinder(size - 4, 3);
  drawFinder(3, size - 4);
  const positions = alignmentPositions(version, size);
  const last = positions.length - 1;
  positions.forEach((x, i) => {
    positions.forEach((y, j) => {
      if ((i === 0 && j === 0) || (i === 0 && j === last) || (i === last && j === 0)) return;
      for (let dy = -2; dy <= 2; dy++) {
        for (let dx = -2; dx <= 2; dx++) setFunction(x + dx, y + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1);
      }
    });
  });
  const drawFormat = (mask: number) => {
    const value = (FORMAT_BITS[level] << 3) | mask;
    let remainder = value;
    for (let i = 0; i < 10; i++) remainder = (remainder << 1) ^ ((remainder >>> 9) * 0x537);
    const format = ((value << 10) | remainder) ^ 0x5412;
    const bit = (i: number) => ((format >>> i) & 1) !== 0;
    for (let i = 0; i <= 5; i++) setFunction(8, i, bit(i));
    setFunction(8, 7, bit(6));
    setFunction(8, 8, bit(7));
    setFunction(7, 8, bit(8));
    for (let i = 9; i < 15; i++) setFunction(14 - i, 8, bit(i));
    for (let i = 0; i < 8; i++) setFunction(size - 1 - i, 8, bit(i));
    for (let i = 8; i < 15; i++) setFunction(8, size - 15 + i, bit(i));
    setFunction(8, size - 8, true);
  };
  drawFormat(0);
  if (version >= 7) {
    let remainder = version;
    for (let i = 0; i < 12; i++) remainder = (remainder << 1) ^ ((remainder >>> 11) * 0x1f25);
    const versionBits = (version << 12) | remainder;
    for (let i = 0; i < 18; i++) {
      const dark = ((versionBits >>> i) & 1) !== 0;
      const a = size - 11 + (i % 3);
      const b = Math.floor(i / 3);
      setFunction(a, b, dark);
      setFunction(b, a, dark);
    }
  }

  // Codewords in the zigzag order; remainder bits stay light.
  let bitIndex = 0;
  for (let right = size - 1; right >= 1; right -= 2) {
    if (right === 6) right = 5;
    for (let vertical = 0; vertical < size; vertical++) {
      for (let j = 0; j < 2; j++) {
        const x = right - j;
        const upward = ((right + 1) & 2) === 0;
        const y = upward ? size - 1 - vertical : vertical;
        if (!reserved[y][x] && bitIndex < codewords.length * 8) {
          modules[y][x] = ((codewords[bitIndex >>> 3] >>> (7 - (bitIndex & 7))) & 1) !== 0;
          bitIndex++;
        }
      }
    }
  }

  const applyMask = (mask: number) => {
    for (let y = 0; y < size; y++) {
      for (let x = 0; x < size; x++) {
        if (!reserved[y][x] && maskApplies(mask, x, y)) modules[y][x] = !modules[y][x];
      }
    }
  };

  const penalty = (): number => {
    let score = 0;
    const scoreLine = (dark: (index: number) => boolean) => {
      // Run history for finder-like 1:1:3:1:1 patterns; the light border
      // around the symbol counts as part of the first and last runs.
      const history = [0, 0, 0, 0, 0, 0, 0];
      const push = (length: number) => {
        if (history[0] === 0) length += size;
        history.pop();
        history.unshift(length);
      };
      const finderPatterns = () => {
        const n = history[1];
        const core = n > 0 && history[2] === n && history[3] === n * 3 && history[4] === n && history[5] === n;
        return (core && history[0] >= n * 4 && history[6] >= n ? 1 : 0) + (core && history[6] >= n * 4 && history[0] >= n ? 1 : 0);
      };
      let runDark = false;
      let runLength = 0;
      for (let index = 0; index < size; index++) {
        if (dark(index) === runDark) {
          runLength++;
          if (runLength === 5) score += PENALTY_RUN;
          else if (runLength > 5) score++;
        } else {
          push(runLength);
          if (!runDark) score += finderPatterns() * PENALTY_FINDER;
          runDark = dark(index);
          runLength = 1;
        }
      }
      if (runDark) {
        push(runLength);
        runLength = 0;
      }
      push(runLength + size);
      score += finderPatterns() * PENALTY_FINDER;
    };
    for (let y = 0; y < size; y++) scoreLine((x) => modules[y][x]);
    for (let x = 0; x < size; x++) scoreLine((y) => modules[y][x]);
    for (let y = 0; y < size - 1; y++) {
      for (let x = 0; x < size - 1; x++) {
        const color = modules[y][x];
        if (color === modules[y][x + 1] && color === modules[y + 1][x] && color === modules[y + 1][x + 1]) score += PENALTY_BLOCK;
      }
    }
    let darkCount = 0;
    for (const row of modules) for (const module of row) if (module) darkCount++;
    const total = size * size;
    score += (Math.ceil(Math.abs(darkCount * 20 - total * 10) / total) - 1) * PENALTY_BALANCE;
    return score;
  };

  let bestMask = forcedMask !== undefined && Number.isInteger(forcedMask) && forcedMask >= 0 && forcedMask < 8 ? forcedMask : -1;
  if (bestMask < 0) {
    let bestScore = Number.POSITIVE_INFINITY;
    for (let mask = 0; mask < 8; mask++) {
      applyMask(mask);
      drawFormat(mask);
      const score = penalty();
      if (score < bestScore) {
        bestMask = mask;
        bestScore = score;
      }
      // Masks are XOR patterns, so applying one twice restores the data.
      applyMask(mask);
    }
  }
  applyMask(bestMask);
  drawFormat(bestMask);
  return { version, size, errorCorrection: level, mask: bestMask, modules };
};

/**
 * SVG path data for the dark modules, offset by a light quiet zone of
 * `border` modules. Horizontal runs are merged to keep the path short.
 */
export const qrSvgPath = (code: QrCode, border = 4): string => {
  let path = "";
  code.modules.forEach((row, y) => {
    for (let x = 0; x < code.size; x++) {
      if (!row[x]) continue;
      let run = 1;
      while (x + run < code.size && row[x + run]) run++;
      path += `M${x + border} ${y + border}h${run}v1h-${run}z`;
      x += run - 1;
    }
  });
  return path;
};
