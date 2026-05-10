import {
  buildOptions,
  get,
  graphqlStandardQuery,
  graphqlVanityQuery,
  graphqlVanitySuggestionQuery,
  postGraphQL,
  standardQuery,
  vanityQuery,
} from './lib.js';

export const options = buildOptions(120);

export default function () {
  const roll = Math.random();

  if (roll < 0.45) {
    get(standardQuery(0.85));
    return;
  }

  if (roll < 0.70) {
    get(vanityQuery(0.85));
    return;
  }

  if (roll < 0.90) {
    postGraphQL(graphqlStandardQuery(0.85));
    return;
  }

  if (roll < 0.95) {
    postGraphQL(graphqlVanityQuery(0.85));
    return;
  }

  postGraphQL(graphqlVanitySuggestionQuery());
}
