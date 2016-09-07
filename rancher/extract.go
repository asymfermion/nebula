package rancher

import (
	"archive/tar"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"io"
	"os"
	"path"
)

func extractTo(dir, tgz string, key []byte) error {
	os.MkdirAll(dir, os.ModePerm)

	file, err := os.Open(tgz)
	if err != nil {
		return err
	}
	defer file.Close()

	var reader io.Reader = file
	if len(key) > 0 {
		iv := make([]byte, aes.BlockSize)
		_, err := reader.Read(iv)
		if err != nil {
			return err
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return err
		}
		stream := cipher.NewCFBDecrypter(block, iv)
		reader = cipher.StreamReader{stream, reader}
	}

	gz, err := gzip.NewReader(reader)
	if err != nil {
		return err
	}

	t := tar.NewReader(gz)
	for {
		header, err := t.Next()
		if err != nil {
			if err == io.EOF {
				break
			} else {
				return err
			}
		}

		switch header.Typeflag {
		case tar.TypeDir:
			err = os.MkdirAll(path.Join(dir, header.Name), os.FileMode(header.Mode))
			if err != nil {
				return err
			}

		case tar.TypeReg:
			writer, err := os.Create(path.Join(dir, header.Name))
			if err != nil {
				return err
			}
			if _, err := io.Copy(writer, t); err != nil {
				writer.Close()
				return err
			}
			if err := os.Chmod(path.Join(dir, header.Name), os.FileMode(header.Mode)); err != nil {
				writer.Close()
				return err
			}

			writer.Close()

		case tar.TypeSymlink:
			os.Symlink(header.Linkname, path.Join(dir, header.Name))
		}
	}

	return nil
}
