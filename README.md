# base64+
base64+ An enhanced base64 Encoder/Decoder, which writes an additional filename, size, hash Header.

Please note: When using the -d (decode) flag you no longer need to provide a file name for the file to been decoded, because it will automatically write the file with the supplied filename in the Header.

Example: $ base64+ -d data.txt (which writes then in the current directory for example example.png or whatever filename was in the Header of the encoded message.

