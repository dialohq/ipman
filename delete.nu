def trash [fileName: string] {
  let dirs = (ls -a **/* | where type == dir | where name =~ $fileName  | where name !~ ".direnv");
  print $dirs
  let inp = (input "Confirm (Y/N): " | str downcase )
  if $inp != "y" {
    print "Aborting..."
    return
  }
  
  let d = ($dirs.name | str join ' ')
  print $"Executing: rm --trash -r ($d)";
  rm --trash -r ($d);
}

trash ".helix"
