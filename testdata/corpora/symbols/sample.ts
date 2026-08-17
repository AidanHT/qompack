export class Parser {
  private pos = 0;

  constructor(private readonly src: string) {
    this.pos = 0;
  }

  parse(): number {
    this.pos += 1;
    return this.pos;
  }

  reset(): void {
    this.pos = 0;
  }
}

export const build = (src: string) => new Parser(src);
