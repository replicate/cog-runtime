// Example function-based predictor
export function simplePredictFunction(input) {
  const multiplier = input.multiplier || 1;
  return {
    original: input.text,
    uppercase: input.text.toUpperCase(),
    length: input.text.length * multiplier,
    timestamp: new Date().toISOString(),
  };
}
