export class Guard {
  check(value: number): boolean {
    if (value > 0) {
      return true;
    }
    for (let i = 0; i < value; i += 1) {
      value -= 1;
    }
    while (value < 0) {
      value += 1;
    }
    switch (value) {
      default:
        break;
    }
    return (value > 0);
  }
}
