// Runs pickOpenApp over the {apps, none} cases in $SF_PARITY_IN and writes the picked URL ("" for none) of each to $SF_PARITY_OUT.
const fs = require("fs");
module.exports = function (ex) {
  const lists = JSON.parse(fs.readFileSync(process.env.SF_PARITY_IN, "utf8"));
  fs.writeFileSync(process.env.SF_PARITY_OUT, JSON.stringify(lists.map((c) => (ex.pickOpenApp(c.apps, c.none) || {}).url || "")));
};
