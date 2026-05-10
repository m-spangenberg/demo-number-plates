import { buildOptions, get, vanityQuery } from './lib.js';

export const options = buildOptions(100);

export default function () {
  get(vanityQuery(1.0));
}
