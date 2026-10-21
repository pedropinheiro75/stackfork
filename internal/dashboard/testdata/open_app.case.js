// Runs pickOpenApp over the app lists in $SF_PARITY_IN and writes the picked URL ("" for none) of each to $SF_PARITY_OUT.
const fs = require("fs");
module.exports = function (ex) {
  const lists = JSON.parse(fs.readFileSync(process.env.SF_PARITY_IN, "utf8"));
  fs.writeFileSync(process.env.SF_PARITY_OUT, JSON.stringify(lists.map((apps) => (ex.pickOpenApp(apps) || {}).url || "")));
};
