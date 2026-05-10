import { buildOptions, graphqlStandardQuery, postGraphQL } from './lib.js';

export const options = buildOptions(100);

export default function () {
  postGraphQL(graphqlStandardQuery(1.0));
}
