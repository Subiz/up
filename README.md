subiz internal deploy script

# installation
1. clone the repository
```git clone https://github.com/subiz/up.git```
2. run install script
```cd up && ./install.sh```


# To deploy new version
1. change version in `stable.txt`, `up.sh`
2. commit the change and add a tag (eg: `git tag -a 4.0.8`)
3. push the change `git push origin master && git push origin 4.0.8`
4. Visit https://github.com/subiz/up/releases/new to create a new release in Github

In client machine, type `up4 update`
