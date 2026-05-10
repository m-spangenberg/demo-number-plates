import { buildOptions, get, standardQuery } from './lib.js';

export const options = buildOptions(100);

export default function () {
  get(standardQuery(1.0));
}
