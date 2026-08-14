// Classic CommonJS Module
function add(a, b) {
  return a + b;
}

function multiply(a, b) {
  return a * b;
}

class Vector2D {
  constructor(x, y) {
    this.x = x;
    this.y = y;
  }

  magnitude() {
    return Math.sqrt(this.x * this.x + this.y * this.y);
  }

  add(other) {
    return new Vector2D(this.x + other.x, this.y + other.y);
  }
}

module.exports = {
  add,
  multiply,
  Vector2D,
  info: {
    format: "commonjs",
    file: typeof __filename !== "undefined" ? __filename : "unknown",
  },
};
